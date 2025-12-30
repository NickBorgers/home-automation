package logbuffer

import (
	"time"

	"go.uber.org/zap/zapcore"
)

// BufferCore is a zap core that writes log entries to a Buffer.
type BufferCore struct {
	buffer  *Buffer
	level   zapcore.LevelEnabler
	encoder zapcore.Encoder
	fields  []zapcore.Field
}

// NewBufferCore creates a new BufferCore that writes to the given buffer.
// It captures log entries at or above the specified level.
func NewBufferCore(buffer *Buffer, level zapcore.LevelEnabler) *BufferCore {
	return &BufferCore{
		buffer: buffer,
		level:  level,
		encoder: zapcore.NewJSONEncoder(zapcore.EncoderConfig{
			MessageKey:     "msg",
			LevelKey:       "level",
			TimeKey:        "ts",
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
		}),
	}
}

// Enabled implements zapcore.Core.
func (c *BufferCore) Enabled(level zapcore.Level) bool {
	return c.level.Enabled(level)
}

// With implements zapcore.Core.
func (c *BufferCore) With(fields []zapcore.Field) zapcore.Core {
	clone := &BufferCore{
		buffer:  c.buffer,
		level:   c.level,
		encoder: c.encoder.Clone(),
		fields:  make([]zapcore.Field, len(c.fields)+len(fields)),
	}
	copy(clone.fields, c.fields)
	copy(clone.fields[len(c.fields):], fields)
	return clone
}

// Check implements zapcore.Core.
func (c *BufferCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return checked.AddCore(entry, c)
	}
	return checked
}

// Write implements zapcore.Core.
func (c *BufferCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Combine context fields and entry fields
	allFields := make([]zapcore.Field, 0, len(c.fields)+len(fields))
	allFields = append(allFields, c.fields...)
	allFields = append(allFields, fields...)

	// Convert fields to map
	fieldMap := make(map[string]interface{}, len(allFields))
	for _, f := range allFields {
		fieldMap[f.Key] = fieldToInterface(f)
	}

	event := Event{
		Timestamp: entry.Time,
		Level:     entry.Level.String(),
		Message:   entry.Message,
		Fields:    fieldMap,
	}

	c.buffer.Add(event)
	return nil
}

// Sync implements zapcore.Core.
func (c *BufferCore) Sync() error {
	return nil
}

// fieldToInterface converts a zapcore.Field to its interface{} value.
func fieldToInterface(f zapcore.Field) interface{} {
	switch f.Type {
	case zapcore.BoolType:
		return f.Integer == 1
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return f.Integer
	case zapcore.Uint64Type, zapcore.Uint32Type, zapcore.Uint16Type, zapcore.Uint8Type:
		return uint64(f.Integer)
	case zapcore.Float64Type:
		return float64(f.Integer)
	case zapcore.Float32Type:
		return float32(f.Integer)
	case zapcore.StringType:
		return f.String
	case zapcore.StringerType:
		if f.Interface != nil {
			return f.Interface
		}
		return nil
	case zapcore.DurationType:
		return time.Duration(f.Integer).String()
	case zapcore.TimeType:
		if f.Interface != nil {
			return f.Interface.(time.Time).Format(time.RFC3339)
		}
		return time.Unix(0, f.Integer).Format(time.RFC3339)
	case zapcore.ReflectType:
		return f.Interface
	default:
		if f.Interface != nil {
			return f.Interface
		}
		return f.String
	}
}

// GetBuffer returns the underlying buffer.
func (c *BufferCore) GetBuffer() *Buffer {
	return c.buffer
}
