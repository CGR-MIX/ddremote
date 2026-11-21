package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const displayJournalName = "super-screen-recovery.json"

type displayJournal struct {
	Version  int             `json:"version"`
	Session  string          `json:"session"`
	Created  time.Time       `json:"created"`
	Original DisplaySnapshot `json:"original"`
}
type displayJournalStore interface {
	Save(displayJournal) error
	Remove() error
}
type fileDisplayJournal struct{ dir string }

func (s fileDisplayJournal) Save(j displayJournal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, displayJournalName)
	// Existing recovery data must be processed before a new transaction starts.
	if _, err = os.Stat(path); err == nil {
		return errors.New("存在尚未恢复的显示布局，请先恢复")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path + ".tmp")
		return err
	}
	if err = os.Rename(path+".tmp", path); err != nil {
		os.Remove(path + ".tmp")
	}
	return err
}
func (s fileDisplayJournal) Remove() error {
	err := os.Remove(filepath.Join(s.dir, displayJournalName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (s fileDisplayJournal) Load() (displayJournal, error) {
	var j displayJournal
	f, err := os.Open(filepath.Join(s.dir, displayJournalName))
	if err != nil {
		return j, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return j, err
	}
	if info.Size() > 1024*1024 {
		return j, errors.New("显示恢复记录过大")
	}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err = dec.Decode(&j); err != nil {
		return j, err
	}
	if j.Version != 1 || !accountNoncePattern.MatchString(j.Session) || len(j.Original.Paths) == 0 || len(j.Original.Modes) == 0 {
		return j, errors.New("显示恢复记录无效")
	}
	return j, nil
}

// The journal is durable BEFORE creating the device or altering any output.
// A failed restore retains the journal; success is never inferred from Close.
type displayTransaction struct {
	journal displayJournal
	store   displayJournalStore
	restore func(DisplaySnapshot) error
	device  interface{ Close() error }
	armed   bool
}

func beginDisplayTransaction(snapshot DisplaySnapshot, session string, store displayJournalStore, restore func(DisplaySnapshot) error) (*displayTransaction, error) {
	if !accountNoncePattern.MatchString(session) || len(snapshot.Paths) == 0 || len(snapshot.Modes) == 0 {
		return nil, errors.New("无法保存原显示布局")
	}
	j := displayJournal{Version: 1, Session: session, Created: time.Now().UTC(), Original: snapshot}
	if err := store.Save(j); err != nil {
		return nil, fmt.Errorf("保存显示恢复记录失败: %w", err)
	}
	return &displayTransaction{journal: j, store: store, restore: restore, armed: true}, nil
}
func (t *displayTransaction) Close() error {
	if !t.armed {
		return nil
	}
	err := t.restore(t.journal.Original)
	if t.device != nil {
		closeErr := t.device.Close()
		t.device = nil
		if closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	// Removing the virtual adapter can unblock restoration on some drivers.
	if err != nil {
		err = t.restore(t.journal.Original)
	}
	if err != nil {
		return fmt.Errorf("原显示布局尚未恢复，已保留恢复记录: %w", err)
	}
	if err = t.store.Remove(); err != nil {
		return err
	}
	t.armed = false
	return nil
}
func recoverDisplayJournal(dir string) error {
	store := fileDisplayJournal{dir: dir}
	journal, err := store.Load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = journal.Original.restore(); err != nil {
		return err
	}
	return store.Remove()
}
