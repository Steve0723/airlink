package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"os"
	"strings"
	"time"
)

const appVersion = "0.1.0"

// State 持久化在 data/state.json，保证重启后二维码里的配对码不变。
type State struct {
	Token       string    `json:"token"`
	DeviceName  string    `json:"deviceName"`
	PreferredIP string    `json:"preferredIP,omitempty"`
	DeviceID    string    `json:"deviceID,omitempty"` // 公网兜底用的设备标识
	RelayURL    string    `json:"relayURL,omitempty"`
	RelayKey    string    `json:"relayKey,omitempty"`
	Phones      []PhoneInfo `json:"phones,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// PhoneInfo 是配对过的手机。用来把文件投递到公网中转时指定收件方。
type PhoneInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	LastSeen time.Time `json:"lastSeen"`
}

func randomToken() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

func loadState(path, defaultName string) (*State, error) {
	st := &State{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, st) == nil && st.Token != "" {
			if st.DeviceName == "" {
				st.DeviceName = defaultName
			}
			if st.DeviceID == "" {
				st.DeviceID = randomToken()[:16]
				_ = saveJSON(path, st)
			}
			return st, nil
		}
	}
	st = &State{
		Token:      randomToken(),
		DeviceName: defaultName,
		DeviceID:   randomToken()[:16],
		CreatedAt:  time.Now(),
	}
	if err := saveJSON(path, st); err != nil {
		return nil, err
	}
	return st, nil
}

// saveJSON 先写临时文件再改名，避免掉电时留下半个文件。
func saveJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// tokenEqual 用恒定时间比较，避免通过响应时间逐字节猜测配对码。
func tokenEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
