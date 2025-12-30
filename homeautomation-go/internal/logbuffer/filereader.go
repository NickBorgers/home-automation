package logbuffer

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"time"
)

// ReadEventsFromFile reads log events from a JSON lines log file.
// It parses each line as a JSON log entry and returns them as Event objects.
// Events are returned in chronological order (oldest first).
// If since is provided (non-zero), only events after that time are returned.
// If limit > 0, at most that many events are returned.
func ReadEventsFromFile(filePath string, since time.Time, limit int) ([]Event, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil // No file yet, return empty
		}
		return nil, err
	}
	defer file.Close()

	return readEventsFromReader(file, since, limit)
}

// readEventsFromReader reads events from an io.Reader.
// This is useful for testing with mock readers.
func readEventsFromReader(reader io.Reader, since time.Time, limit int) ([]Event, error) {
	scanner := bufio.NewScanner(reader)
	// Increase buffer size for long log lines
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var events []Event

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		event, err := parseLogLine(line)
		if err != nil {
			// Skip malformed lines
			continue
		}

		// Filter by timestamp
		if !since.IsZero() && !event.Timestamp.After(since) {
			continue
		}

		events = append(events, event)

		// Check limit
		if limit > 0 && len(events) >= limit {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

// parseLogLine parses a single JSON log line into an Event.
func parseLogLine(line []byte) (Event, error) {
	// First parse into a map to get all fields
	var rawMap map[string]interface{}
	if err := json.Unmarshal(line, &rawMap); err != nil {
		return Event{}, err
	}

	event := Event{
		Fields: make(map[string]interface{}),
	}

	// Extract standard fields
	if level, ok := rawMap["level"].(string); ok {
		event.Level = level
		delete(rawMap, "level")
	}

	if msg, ok := rawMap["msg"].(string); ok {
		event.Message = msg
		delete(rawMap, "msg")
	}

	// Parse timestamp - zap uses ISO8601 format
	if ts, ok := rawMap["ts"].(string); ok {
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			// Try alternative formats
			parsed, err = time.Parse("2006-01-02T15:04:05.000Z0700", ts)
			if err != nil {
				parsed, err = time.Parse("2006-01-02T15:04:05Z07:00", ts)
			}
		}
		if err == nil {
			event.Timestamp = parsed
		}
		delete(rawMap, "ts")
	}

	// All remaining fields go into Fields map
	for k, v := range rawMap {
		event.Fields[k] = v
	}

	return event, nil
}

// ReadEventsFromFileReverse reads the last N events from a log file.
// This is more efficient when you only need recent events from a large file.
// Events are returned in chronological order (oldest first).
func ReadEventsFromFileReverse(filePath string, maxEvents int) ([]Event, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Event{}, nil
		}
		return nil, err
	}
	defer file.Close()

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	fileSize := stat.Size()
	if fileSize == 0 {
		return []Event{}, nil
	}

	// Read the file in chunks from the end
	const chunkSize = 64 * 1024 // 64KB chunks
	var lines [][]byte
	var partialLine []byte
	position := fileSize

	for position > 0 && len(lines) < maxEvents {
		// Calculate chunk position
		readSize := int64(chunkSize)
		if position < readSize {
			readSize = position
		}
		position -= readSize

		// Seek and read chunk
		_, err := file.Seek(position, io.SeekStart)
		if err != nil {
			return nil, err
		}

		chunk := make([]byte, readSize)
		_, err = io.ReadFull(file, chunk)
		if err != nil {
			return nil, err
		}

		// Append any partial line from previous chunk
		if len(partialLine) > 0 {
			chunk = append(chunk, partialLine...)
			partialLine = nil
		}

		// Split into lines
		chunkLines := splitLinesReverse(chunk)

		// First line in chunk might be partial (its beginning is in an earlier chunk)
		// Save it to prepend to the next chunk we read
		if position > 0 && len(chunkLines) > 0 {
			partialLine = chunkLines[0]
			chunkLines = chunkLines[1:]
		}

		lines = append(chunkLines, lines...)
	}

	// If we have a remaining partial line and we've read from the start
	if position == 0 && len(partialLine) > 0 {
		lines = append([][]byte{partialLine}, lines...)
	}

	// Keep only the last maxEvents lines
	if len(lines) > maxEvents {
		lines = lines[len(lines)-maxEvents:]
	}

	// Parse lines into events
	var events []Event
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		event, err := parseLogLine(line)
		if err != nil {
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// splitLinesReverse splits a byte slice into lines, preserving order.
func splitLinesReverse(data []byte) [][]byte {
	var lines [][]byte
	start := 0

	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}

	// Handle last line (no trailing newline)
	if start < len(data) {
		lines = append(lines, data[start:])
	}

	return lines
}
