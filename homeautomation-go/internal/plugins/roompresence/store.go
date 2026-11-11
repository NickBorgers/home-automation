package roompresence

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// maxStoreLen is the longest value an input_text helper can hold.
const maxStoreLen = 255

// storeSettle is how long a token must stay in an unseen area before a change saves it. Frigate often
// loses a person who stands in a doorway zone and finds them again a second later, which moves a token
// through the doorway and back. Without this, each of those writes the helper twice.
const storeSettle = 10 * time.Second

// storedTokens is the token store's JSON. Times are Unix seconds, to fit more tokens in 255 characters:
// {"t":1793440000,"k":[["bedroom",1793430100]]}
type storedTokens struct {
	SavedAt int64           `json:"t"`
	Tokens  [][]interface{} `json:"k"`
}

// encodeTokens writes kept tokens as the store's JSON. Tokens that do not fit in 255 characters are left
// out, newest first, and counted in dropped.
func encodeTokens(savedAt time.Time, kept []KeptToken) (value string, dropped int, err error) {
	s := storedTokens{SavedAt: savedAt.Unix(), Tokens: [][]interface{}{}}
	for _, k := range kept {
		s.Tokens = append(s.Tokens, []interface{}{k.Area, k.Since.Unix()})
	}
	for {
		b, err := json.Marshal(s)
		if err != nil {
			return "", 0, err
		}
		if len(b) <= maxStoreLen || len(s.Tokens) == 0 {
			return string(b), dropped, nil
		}
		// The oldest tokens have stayed longest, so they are the likeliest to still be there.
		newest := 0
		for i, tok := range s.Tokens {
			if tok[1].(int64) > s.Tokens[newest][1].(int64) {
				newest = i
			}
		}
		s.Tokens = append(s.Tokens[:newest], s.Tokens[newest+1:]...)
		dropped++
	}
}

// decodeTokens reads the store's JSON.
func decodeTokens(value string) (time.Time, []KeptToken, error) {
	var raw struct {
		SavedAt int64               `json:"t"`
		Tokens  [][]json.RawMessage `json:"k"`
	}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return time.Time{}, nil, err
	}
	var kept []KeptToken
	for _, tok := range raw.Tokens {
		if len(tok) != 2 {
			return time.Time{}, nil, fmt.Errorf("token %v: want [area, since]", tok)
		}
		var k KeptToken
		var since int64
		if err := json.Unmarshal(tok[0], &k.Area); err != nil {
			return time.Time{}, nil, fmt.Errorf("token area: %w", err)
		}
		if err := json.Unmarshal(tok[1], &since); err != nil {
			return time.Time{}, nil, fmt.Errorf("token since: %w", err)
		}
		k.Since = time.Unix(since, 0)
		kept = append(kept, k)
	}
	return time.Unix(raw.SavedAt, 0), kept, nil
}

// restoreTokens loads the token store at startup. A missing, unreadable or old copy loads nothing.
func (m *Manager) restoreTokens() {
	if m.cfg.TokenStore == "" {
		return
	}
	s, err := m.haClient.GetState(m.cfg.TokenStore)
	if err != nil || s == nil || s.State == "" || s.State == "unknown" || s.State == "unavailable" {
		m.logger.Info("No kept room presence tokens", zap.String("entity_id", m.cfg.TokenStore), zap.Error(err))
		return
	}
	savedAt, kept, err := decodeTokens(s.State)
	if err != nil {
		m.logger.Warn("Cannot read kept room presence tokens", zap.String("entity_id", m.cfg.TokenStore),
			zap.Error(err))
		return
	}
	if age := m.now().Sub(savedAt); age > m.cfg.TokenStoreMaxAge {
		m.logger.Info("Kept room presence tokens are too old; starting with none",
			zap.Duration("age", age), zap.Duration("max_age", m.cfg.TokenStoreMaxAge))
		return
	}
	var n int
	m.apply(func(t *Tracker, now time.Time) { n = t.Restore(now, kept) })
	m.logger.Info("Restored room presence tokens", zap.Int("tokens", n), zap.Time("saved_at", savedAt))
}

// storeTokens writes the unseen tokens to the token store. It writes when the settled tokens changed
// since the last write, every third of token_store_max_age while any are kept, and always when force is
// set. The regular write keeps the save time fresh, so the copy still loads after the process is killed
// without a clean stop. It writes in every mode, because nothing but this plugin reads the store, and
// never in read-only mode. The caller holds applyMu.
func (m *Manager) storeTokens(now time.Time, kept []KeptToken, force bool) {
	if m.cfg.TokenStore == "" || m.readOnly {
		return
	}
	if !force {
		kept = settled(now, kept)
	}
	key, _, err := encodeTokens(time.Unix(0, 0), kept) // the tokens alone, to see if they changed
	if err != nil {
		return
	}
	due := len(kept) > 0 && now.Sub(m.storedAt) >= m.cfg.TokenStoreMaxAge/3
	if !force && !due && m.storedKey == key {
		return
	}
	value, dropped, err := encodeTokens(now, kept)
	if err != nil {
		m.logger.Error("Cannot encode room presence tokens", zap.Error(err))
		return
	}
	if dropped > 0 {
		m.logger.Warn("Room presence token store is full; newest tokens not kept", zap.Int("dropped", dropped))
	}
	if err := m.haClient.SetInputText(strings.TrimPrefix(m.cfg.TokenStore, "input_text."), value); err != nil {
		m.logger.Error("Cannot write room presence tokens", zap.String("entity_id", m.cfg.TokenStore),
			zap.Error(err))
		return // try again on the next change
	}
	m.storedKey = key
	m.storedAt = now
}

// settled leaves out tokens that arrived in their area less than storeSettle ago.
func settled(now time.Time, kept []KeptToken) []KeptToken {
	var out []KeptToken
	for _, k := range kept {
		if now.Sub(k.Since) >= storeSettle {
			out = append(out, k)
		}
	}
	return out
}
