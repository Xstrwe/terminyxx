package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"terminyxx/internal/connection"
	"terminyxx/internal/dragout"
)

// Dragging remote files out to Explorer: the UI calls SFTPPrepareDragOut as
// soon as a drag of server rows begins (the tree is listed in the background),
// then SFTPStartDragOut when the pointer leaves the window with the button
// still held. The files are streamed from the server when they are dropped.

type dragPlan struct {
	connID string
	items  []connection.DragItem
	err    error
	ready  chan struct{}
	cancel context.CancelFunc
	born   time.Time
}

var (
	dragMu    sync.Mutex
	dragPlans = map[string]*dragPlan{}
)

const dragPlanTTL = 5 * time.Minute

// SFTPPrepareDragOut starts listing the selection for a drag out and returns
// a token for SFTPStartDragOut / SFTPCancelDragOut.
func (a *App) SFTPPrepareDragOut(id string, sources []string) (string, error) {
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return "", connection.ErrLocked
	}
	if len(sources) == 0 {
		return "", errors.New("nothing to drag")
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	ctx, cancel := context.WithTimeout(context.Background(), dragPlanTTL)
	p := &dragPlan{connID: id, ready: make(chan struct{}), cancel: cancel, born: time.Now()}

	dragMu.Lock()
	for t, old := range dragPlans { // drop plans nobody started
		if time.Since(old.born) > dragPlanTTL {
			old.cancel()
			delete(dragPlans, t)
		}
	}
	dragPlans[token] = p
	dragMu.Unlock()

	go func() {
		defer close(p.ready)
		p.items, p.err = a.connMgr.PlanDragOut(ctx, id, sources)
	}()
	return token, nil
}

// SFTPCancelDragOut forgets a prepared drag (the pointer never left the window).
func (a *App) SFTPCancelDragOut(token string) {
	dragMu.Lock()
	p := dragPlans[token]
	delete(dragPlans, token)
	dragMu.Unlock()
	if p != nil {
		p.cancel()
	}
}

// SFTPStartDragOut begins the OS drag for a prepared selection. The mouse
// button must still be down. It returns when the drag ends: true if the files
// were dropped on another application.
func (a *App) SFTPStartDragOut(token string) (bool, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return false, connection.ErrLocked
	}
	dragMu.Lock()
	p := dragPlans[token]
	delete(dragPlans, token)
	dragMu.Unlock()
	if p == nil {
		return false, errors.New("drag expired")
	}
	defer p.cancel()

	select {
	case <-p.ready:
	case <-time.After(10 * time.Second):
		return false, errors.New("listing the selected files is taking too long")
	}
	if p.err != nil {
		return false, p.err
	}

	items := make([]dragout.Item, len(p.items))
	for i, it := range p.items {
		items[i] = dragout.Item{Name: strings.ReplaceAll(it.Rel, "/", `\`), IsDir: it.IsDir, Size: it.Size, ModTime: it.ModTime}
	}
	return dragout.Start(items, func(i int) (io.ReadCloser, error) {
		if a.sec.lock != nil && a.sec.lock.Locked() {
			return nil, connection.ErrLocked
		}
		return a.connMgr.OpenDragItem(p.connID, p.items[i])
	})
}
