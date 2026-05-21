package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zalando/go-keyring"
)

// Keychain identifiers for the stored SIP secret.
const (
	keyringService = "Kiskeya"
	keyringUser    = "sip-password"
)

// Contact represents a phone contact.
type Contact struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SIPAddress string `json:"sipAddress"`
}

// CallLog represents a historic call log entry.
type CallLog struct {
	ID          string    `json:"id"`
	Number      string    `json:"number"`
	Direction   string    `json:"direction"`   // "incoming" or "outgoing"
	Status      string    `json:"status"`      // "answered", "missed", "rejected", "failed"
	Timestamp   time.Time `json:"timestamp"`
	DurationSec int       `json:"durationSec"` // Call duration in seconds
}

// App struct manages state and coordination.
type App struct {
	ctx         context.Context
	audioEngine *AudioEngine
	sipEngine   *SIPEngine

	// Local Storage
	contacts    []Contact
	callLogs    []CallLog
	storageDir  string
	storageMu   sync.Mutex

	// emit, when set, overrides Wails event emission (for headless tests).
	emit func(name string, data interface{})
}

// NewApp creates a new App application struct
func NewApp() *App {
	// Resolve storage path (~/.kiskeya)
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	storageDir := filepath.Join(home, ".kiskeya")
	_ = os.MkdirAll(storageDir, 0755)

	app := &App{
		storageDir: storageDir,
		contacts:   []Contact{},
		callLogs:   []CallLog{},
	}

	app.loadContacts()
	app.loadCallHistory()

	return app
}

// GetVersion returns the application version to the frontend.
func (a *App) GetVersion() string {
	return AppVersion
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize engines
	ae, err := NewAudioEngine(a)
	if err != nil {
		println("ERROR: Audio Engine initialization failed:", err.Error())
		return
	}
	a.audioEngine = ae

	se, err := NewSIPEngine(a, ae)
	if err != nil {
		println("ERROR: SIP Engine initialization failed:", err.Error())
		return
	}
	a.sipEngine = se

	// Start local SIP server (port 5060 by default)
	err = a.sipEngine.StartServer(5060)
	if err != nil {
		println("ERROR: SIP Server start failed:", err.Error())
	}

	// Listen for call state updates to append to history. The SIP engine computes
	// the log fields before clearing call state and ships them in the idle event,
	// so we read them straight from the payload (the live struct is already reset).
	runtime.EventsOn(a.ctx, "sip:call_state", func(optionalData ...interface{}) {
		if len(optionalData) == 0 {
			return
		}
		data, ok := optionalData[0].(map[string]interface{})
		if !ok {
			return
		}
		if state, _ := data["state"].(string); state != "idle" {
			return
		}

		number, _ := data["logNumber"].(string)
		if number == "" {
			return
		}
		direction, _ := data["logDirection"].(string)
		status, _ := data["logStatus"].(string)

		// Numbers may arrive as int (Go listener) or float64 (JSON round-trip).
		duration := 0
		switch v := data["logDuration"].(type) {
		case int:
			duration = v
		case int64:
			duration = int(v)
		case float64:
			duration = int(v)
		}

		a.AddCallLogEntry(number, direction, status, duration)
	})
}

// shutdown is called when the application closes.
func (a *App) shutdown(ctx context.Context) {
	if a.sipEngine != nil {
		a.sipEngine.Close()
	}
	if a.audioEngine != nil {
		a.audioEngine.Close()
	}
}

// EmitEvent sends an asynchronous event to the JS frontend. If an emit hook is
// set (used by integration tests / headless runs) it takes precedence over the
// Wails runtime.
func (a *App) EmitEvent(name string, data interface{}) {
	if a.emit != nil {
		a.emit(name, data)
		return
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data)
	}
}

// --- SIP Bindings ---

func (a *App) RegisterSIP(displayName, username, password, domain, proxy, protocol, stunServer string, mediaEncryption, allowInsecureTLS bool) string {
	if a.sipEngine == nil {
		return "SIP engine uninitialized"
	}
	acc := &SIPAccount{
		DisplayName:      displayName,
		Username:         username,
		Password:         password,
		Domain:           domain,
		Proxy:            proxy,
		Protocol:         protocol,
		STUNServer:       stunServer,
		MediaEncryption:  mediaEncryption,
		AllowInsecureTLS: allowInsecureTLS,
	}
	a.sipEngine.Register(acc)
	return "registering"
}

func (a *App) UnregisterSIP() string {
	if a.sipEngine == nil {
		return "SIP engine uninitialized"
	}
	a.sipEngine.Unregister()
	return "idle"
}

// --- Account Persistence Bindings (OS keychain for the secret) ---

// SaveAccount persists the SIP profile: the password goes to the OS keychain
// (macOS Keychain / Windows Credential Manager / Linux Secret Service) and the
// non-secret fields go to ~/.kiskeya/account.json (0600). Returns an empty
// string on success or an error message if the keychain write failed (in which
// case the password is NOT written to disk).
func (a *App) SaveAccount(displayName, username, password, domain, proxy, protocol, stunServer string, mediaEncryption, allowInsecureTLS bool) string {
	keyErr := ""
	passwordSaved := false
	if password != "" {
		if err := keyring.Set(keyringService, keyringUser, password); err != nil {
			keyErr = err.Error()
		} else {
			passwordSaved = true
		}
	} else {
		// Empty password: clear any previously stored secret.
		_ = keyring.Delete(keyringService, keyringUser)
	}

	cfg := map[string]interface{}{
		"displayName":        displayName,
		"username":           username,
		"domain":             domain,
		"proxy":              proxy,
		"protocol":           protocol,
		"stunServer":         stunServer,
		"mediaEncryption":    mediaEncryption,
		"allowInsecureTLS":   allowInsecureTLS,
		"passwordInKeychain": passwordSaved,
	}

	a.storageMu.Lock()
	defer a.storageMu.Unlock()
	path := filepath.Join(a.storageDir, "account.json")
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(path, data, 0600)

	return keyErr
}

// LoadAccount returns the saved SIP profile, reading the password back from the
// OS keychain. The returned map mirrors the SaveAccount fields plus "password".
func (a *App) LoadAccount() map[string]interface{} {
	a.storageMu.Lock()
	path := filepath.Join(a.storageDir, "account.json")
	data, err := os.ReadFile(path)
	a.storageMu.Unlock()

	cfg := map[string]interface{}{}
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	if pw, err := keyring.Get(keyringService, keyringUser); err == nil {
		cfg["password"] = pw
	} else {
		cfg["password"] = ""
	}
	return cfg
}

// ClearAccount removes the stored profile and the keychain secret.
func (a *App) ClearAccount() {
	_ = keyring.Delete(keyringService, keyringUser)
	a.storageMu.Lock()
	defer a.storageMu.Unlock()
	_ = os.Remove(filepath.Join(a.storageDir, "account.json"))
}

func (a *App) MakeCall(destination string) string {
	if a.sipEngine == nil {
		return "SIP engine uninitialized"
	}
	err := a.sipEngine.MakeCall(destination)
	if err != nil {
		return err.Error()
	}
	return "dialing"
}

func (a *App) AnswerCall() string {
	if a.sipEngine == nil {
		return "SIP engine uninitialized"
	}
	err := a.sipEngine.AnswerCall()
	if err != nil {
		return err.Error()
	}
	return "connected"
}

func (a *App) RejectCall() string {
	if a.sipEngine == nil {
		return "SIP engine uninitialized"
	}
	err := a.sipEngine.RejectCall()
	if err != nil {
		return err.Error()
	}
	return "rejected"
}

func (a *App) HangupCall() string {
	if a.sipEngine == nil {
		return "SIP engine uninitialized"
	}
	err := a.sipEngine.HangupCall()
	if err != nil {
		return err.Error()
	}
	return "hungup"
}

func (a *App) MuteMicrophone(mute bool) {
	if a.audioEngine != nil {
		a.audioEngine.MuteMicrophone(mute)
	}
}

// SendDTMF transmits a DTMF digit (0-9, *, #, A-D) in-band as an RFC 4733
// telephone-event during an active call.
func (a *App) SendDTMF(digit string) string {
	if a.audioEngine == nil {
		return "audio engine uninitialized"
	}
	if err := a.audioEngine.SendDTMF(digit); err != nil {
		return err.Error()
	}
	return "ok"
}

// --- Audio Device Bindings ---

func (a *App) GetAudioDevices() map[string]interface{} {
	if a.audioEngine == nil {
		return map[string]interface{}{"mics": []AudioDevice{}, "speakers": []AudioDevice{}, "error": "audio engine uninitialized"}
	}
	mics, speakers, err := a.audioEngine.GetDevices()
	if err != nil {
		return map[string]interface{}{"mics": []AudioDevice{}, "speakers": []AudioDevice{}, "error": err.Error()}
	}
	return map[string]interface{}{
		"mics":     mics,
		"speakers": speakers,
		"error":    "",
	}
}

func (a *App) SetAudioDevices(micID, speakerID string) {
	if a.audioEngine != nil {
		a.audioEngine.SetDevices(micID, speakerID)
	}
}

// --- Contact Management Bindings ---

func (a *App) GetContacts() []Contact {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()
	// Return a copy: the result is JSON-marshaled by Wails on another goroutine,
	// which must not race with later mutations of the backing array.
	return append([]Contact(nil), a.contacts...)
}

func (a *App) SaveContact(name, sipAddress string) []Contact {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()

	// Check if contact already exists by address, or generate new
	found := false
	for i, c := range a.contacts {
		if c.SIPAddress == sipAddress {
			a.contacts[i].Name = name
			found = true
			break
		}
	}

	if !found {
		a.contacts = append(a.contacts, Contact{
			ID:         uuid.New().String(),
			Name:       name,
			SIPAddress: sipAddress,
		})
	}

	a.saveContactsUnlocked()
	return a.contacts
}

func (a *App) DeleteContact(id string) []Contact {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()

	for i, c := range a.contacts {
		if c.ID == id {
			a.contacts = append(a.contacts[:i], a.contacts[i+1:]...)
			break
		}
	}

	a.saveContactsUnlocked()
	return a.contacts
}

// --- Call History Bindings ---

func (a *App) GetCallHistory() []CallLog {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()
	// Return a copy (see GetContacts) to avoid racing the marshaler with mutations.
	return append([]CallLog(nil), a.callLogs...)
}

func (a *App) AddCallLogEntry(number, direction, status string, durationSec int) {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()

	log := CallLog{
		ID:          uuid.New().String(),
		Number:      number,
		Direction:   direction,
		Status:      status,
		Timestamp:   time.Now(),
		DurationSec: durationSec,
	}

	// Keep history to last 100 calls
	a.callLogs = append([]CallLog{log}, a.callLogs...)
	if len(a.callLogs) > 100 {
		a.callLogs = a.callLogs[:100]
	}

	a.saveCallHistoryUnlocked()
	a.EmitEvent("history:updated", a.callLogs)
}

func (a *App) ClearCallHistory() []CallLog {
	a.storageMu.Lock()
	defer a.storageMu.Unlock()
	a.callLogs = []CallLog{}
	a.saveCallHistoryUnlocked()
	return a.callLogs
}

// --- Storage Handlers ---

func (a *App) loadContacts() {
	path := filepath.Join(a.storageDir, "contacts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &a.contacts)
}

func (a *App) saveContactsUnlocked() {
	path := filepath.Join(a.storageDir, "contacts.json")
	data, _ := json.Marshal(a.contacts)
	_ = os.WriteFile(path, data, 0644)
}

func (a *App) loadCallHistory() {
	path := filepath.Join(a.storageDir, "history.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &a.callLogs)
}

func (a *App) saveCallHistoryUnlocked() {
	path := filepath.Join(a.storageDir, "history.json")
	data, _ := json.Marshal(a.callLogs)
	_ = os.WriteFile(path, data, 0644)
}
