// Package valetudotest is a fake Valetudo robot for tests. It serves the REST endpoints the valetudo client
// uses, records every command, and lets a test move the robot and drain its battery.
package valetudotest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Command is one PUT the fake received.
type Command struct {
	Path string
	Body map[string]any
}

// Robot is a fake robot behind an httptest server.
type Robot struct {
	*httptest.Server

	mu        sync.Mutex
	battery   int
	status    string
	flag      string
	presets   map[string]string
	pixelSize float64
	robot     *[2]float64
	charger   *[2]float64
	segments  map[string]string // id -> name
	commands  []Command
	fail      map[string]int // path -> HTTP status to answer with
}

// New starts a fake robot that is docked at 100 % with a 5 cm pixel. Close it when done.
func New() *Robot {
	r := &Robot{
		battery:   100,
		status:    "docked",
		flag:      "none",
		presets:   map[string]string{},
		pixelSize: 5,
		segments:  map[string]string{},
		fail:      map[string]int{},
	}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	return r
}

// SetBattery sets the battery level in percent.
func (r *Robot) SetBattery(level int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.battery = level
}

// SetStatus sets the status value and flag.
func (r *Robot) SetStatus(status, flag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status, r.flag = status, flag
}

// SetPreset sets a preset value, such as "operation_mode" to "vacuum".
func (r *Robot) SetPreset(typ, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.presets[typ] = value
}

// SetPixelSize sets the map pixel size in centimetres.
func (r *Robot) SetPixelSize(cm float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pixelSize = cm
}

// SetRobotPixel places the robot at a map pixel. A nil-like call with ok false removes it.
func (r *Robot) SetRobotPixel(x, y float64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !ok {
		r.robot = nil
		return
	}
	r.robot = &[2]float64{x * r.pixelSize, y * r.pixelSize}
}

// SetChargerPixel places the dock at a map pixel.
func (r *Robot) SetChargerPixel(x, y float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.charger = &[2]float64{x * r.pixelSize, y * r.pixelSize}
}

// AddSegment adds a segment layer.
func (r *Robot) AddSegment(id, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.segments[id] = name
}

// Fail makes every request to path answer with an HTTP status. 0 stops failing.
func (r *Robot) Fail(path string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if status == 0 {
		delete(r.fail, path)
		return
	}
	r.fail[path] = status
}

// Commands returns a copy of the commands received so far.
func (r *Robot) Commands() []Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Command(nil), r.commands...)
}

// ClearCommands forgets the commands received so far.
func (r *Robot) ClearCommands() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = nil
}

func (r *Robot) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if code, ok := r.fail[req.URL.Path]; ok {
		http.Error(w, "fake failure", code)
		return
	}
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/api/v2/robot/state/attributes":
		attrs := []map[string]any{
			{"__class": "BatteryStateAttribute", "level": r.battery, "flag": "none"},
			{"__class": "StatusStateAttribute", "value": r.status, "flag": r.flag},
		}
		for typ, v := range r.presets {
			attrs = append(attrs, map[string]any{"__class": "PresetSelectionStateAttribute", "type": typ, "value": v})
		}
		writeJSON(w, attrs)
	case req.Method == http.MethodGet && req.URL.Path == "/api/v2/robot/state/map":
		var entities []map[string]any
		if r.robot != nil {
			entities = append(entities, map[string]any{"__class": "PointMapEntity", "type": "robot_position",
				"points": []float64{r.robot[0], r.robot[1]}, "metaData": map[string]any{"angle": 0}})
		}
		if r.charger != nil {
			entities = append(entities, map[string]any{"__class": "PointMapEntity", "type": "charger_location",
				"points": []float64{r.charger[0], r.charger[1]}})
		}
		var layers []map[string]any
		for id, name := range r.segments {
			layers = append(layers, map[string]any{"__class": "MapLayer", "type": "segment",
				"metaData": map[string]any{"segmentId": id, "name": name}, "compressedPixels": []int{0, 0, 1}})
		}
		writeJSON(w, map[string]any{"__class": "ValetudoMap", "pixelSize": r.pixelSize, "layers": layers,
			"entities": entities})
	case req.Method == http.MethodGet && req.URL.Path == "/api/v2/robot/capabilities/MapSegmentationCapability":
		segs := []map[string]string{}
		for id, name := range r.segments {
			segs = append(segs, map[string]string{"__class": "ValetudoMapSegment", "id": id, "name": name})
		}
		writeJSON(w, segs)
	case req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/api/v2/robot/capabilities/"):
		data, _ := io.ReadAll(req.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		r.commands = append(r.commands, Command{Path: req.URL.Path, Body: body})
		r.apply(req.URL.Path, body)
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, req)
	}
}

// apply moves the status the way a robot would. The caller holds mu.
func (r *Robot) apply(path string, body map[string]any) {
	switch {
	case strings.HasSuffix(path, "/MapSegmentationCapability"):
		r.status, r.flag = "cleaning", "segment"
	case strings.HasSuffix(path, "/BasicControlCapability"):
		switch body["action"] {
		case "home":
			r.status, r.flag = "returning", "none"
		case "pause":
			r.status, r.flag = "paused", "resumable"
		case "stop":
			r.status, r.flag = "idle", "none"
		case "start":
			r.status, r.flag = "cleaning", "none"
		}
	case strings.HasSuffix(path, "/FanSpeedControlCapability/preset"):
		r.presets["fan_speed"], _ = body["name"].(string)
	case strings.HasSuffix(path, "/WaterUsageControlCapability/preset"):
		r.presets["water_grade"], _ = body["name"].(string)
	case strings.HasSuffix(path, "/OperationModeControlCapability/preset"):
		r.presets["operation_mode"], _ = body["name"].(string)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
