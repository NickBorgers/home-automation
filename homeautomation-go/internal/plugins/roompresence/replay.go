package roompresence

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// CapturedMessage is one line of a replay log: an MQTT message and the time it arrived, in Unix seconds.
// It is the format scripts that capture mqtt/subscribe output write, one JSON object per line.
type CapturedMessage struct {
	T       float64 `json:"t"`
	Topic   string  `json:"topic"`
	Payload string  `json:"payload"`
}

// ReplayExpectations are the checks for one replay log, read from <log>.expect.yaml.
type ReplayExpectations struct {
	// Checkpoints are checked in order, each at a time measured from the first message.
	Checkpoints []ReplayCheckpoint `yaml:"checkpoints"`

	// UnknownZones, when set, must equal the zones the log used that match no area. An empty list
	// proves the config covers every zone in the log.
	UnknownZones *[]string `yaml:"unknown_zones"`
}

// ReplayCheckpoint lists areas that must be occupied or empty at one moment.
type ReplayCheckpoint struct {
	After    float64  `yaml:"after"` // seconds after the first message
	Note     string   `yaml:"note"`
	Occupied []string `yaml:"occupied"`
	Empty    []string `yaml:"empty"`
}

// ReadCapture reads a replay log.
func ReadCapture(r io.Reader) ([]CapturedMessage, error) {
	var msgs []CapturedMessage
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for line := 1; sc.Scan(); line++ {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var m CapturedMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		msgs = append(msgs, m)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].T < msgs[j].T })
	return msgs, nil
}

// ParseExpectations reads a .expect.yaml file.
func ParseExpectations(data []byte) (*ReplayExpectations, error) {
	var e ReplayExpectations
	if err := yaml.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parse expectations: %w", err)
	}
	return &e, nil
}

// Replay feeds a log through a new tracker with a fake clock that ticks once a second, and checks the
// expectations. It returns one line per failed check, and the tracker for further inspection.
func Replay(cfg *Config, msgs []CapturedMessage, exp *ReplayExpectations) ([]string, *Tracker) {
	tr := NewTracker(cfg)
	if len(msgs) == 0 {
		return nil, tr
	}
	start := unixTime(msgs[0].T)
	checkpoints := append([]ReplayCheckpoint(nil), exp.Checkpoints...)
	sort.SliceStable(checkpoints, func(i, j int) bool { return checkpoints[i].After < checkpoints[j].After })

	var failures []string
	check := func(cp ReplayCheckpoint) {
		for _, a := range cp.Occupied {
			if !tr.Occupied(a) {
				failures = append(failures, fmt.Sprintf("at +%.1fs (%s): %s should be occupied", cp.After, cp.Note, a))
			}
		}
		for _, a := range cp.Empty {
			if tr.Occupied(a) {
				failures = append(failures, fmt.Sprintf("at +%.1fs (%s): %s should be empty", cp.After, cp.Note, a))
			}
		}
	}

	nextTick := start.Add(tickInterval)
	advance := func(to time.Time) {
		for !nextTick.After(to) {
			for len(checkpoints) > 0 && !start.Add(seconds(checkpoints[0].After)).After(nextTick) {
				check(checkpoints[0])
				checkpoints = checkpoints[1:]
			}
			tr.Tick(nextTick)
			nextTick = nextTick.Add(tickInterval)
		}
	}

	for _, m := range msgs {
		now := unixTime(m.T)
		advance(now)
		for len(checkpoints) > 0 && !start.Add(seconds(checkpoints[0].After)).After(now) {
			check(checkpoints[0])
			checkpoints = checkpoints[1:]
		}
		switch cam, isHeartbeat := heartbeatCamera(m.Topic); {
		case m.Topic == eventsTopic:
			var ev FrigateEvent
			if err := json.Unmarshal([]byte(m.Payload), &ev); err != nil {
				failures = append(failures, fmt.Sprintf("at %.3f: cannot parse event: %v", m.T, err))
				continue
			}
			tr.HandleEvent(now, ev)
		case isHeartbeat && m.Payload == "online":
			tr.Heartbeat(now, cam)
		}
	}
	// Checkpoints after the last message: advance far enough that the tick loop checks each of them.
	if n := len(checkpoints); n > 0 {
		advance(start.Add(seconds(checkpoints[n-1].After) + tickInterval))
	}

	if exp.UnknownZones != nil {
		got := tr.Snapshot().UnknownZones
		want := append([]string{}, *exp.UnknownZones...)
		sort.Strings(want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			failures = append(failures, fmt.Sprintf("unknown zones: got %v, want %v", got, want))
		}
	}
	return failures, tr
}

func unixTime(t float64) time.Time {
	sec, frac := math.Modf(t)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
