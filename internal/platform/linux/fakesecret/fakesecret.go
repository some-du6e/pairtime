// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

// Package fakesecret is a Secret Service for tests. It owns the name
// org.freedesktop.secrets on the session bus and keeps items in memory,
// implementing the part of the freedesktop.org Secret Service API that
// go-keyring v0.2.8 calls: Service.OpenSession and Service.Unlock, the
// Service's Collections property, Collection.SearchItems and
// Collection.CreateItem, Item.GetSecret and Item.Delete, Session.Close,
// and Prompt.Prompt. It prompts only when told to dismiss deletes, and it
// supports only the "plain" session algorithm, as go-keyring does.
//
// The tests that use it are *_desktop_test.go files, which platform.yml
// runs inside a private bus from dbus-run-session (AGENTS.md), so it never
// meets a real keyring. Start never takes the name from another owner: it
// waits for the owner to give it up, as another test binary's fake does
// when its test ends, and fails when that takes longer than NameWait.
package fakesecret

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// NameWait is how long Start waits for another owner of
// org.freedesktop.secrets to give it up. go test runs packages' test
// binaries side by side unless told -p 1, and each may start a fake on the
// same bus, so a fake can find another holding the name for the length of
// one test.
const NameWait = 30 * time.Second

const (
	busName         = "org.freedesktop.secrets"
	servicePath     = dbus.ObjectPath("/org/freedesktop/secrets")
	loginPath       = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	sessionPrefix   = "/org/freedesktop/secrets/session/"
	promptPrefix    = "/org/freedesktop/secrets/prompt/"
	ifaceService    = "org.freedesktop.Secret.Service"
	ifaceCollection = "org.freedesktop.Secret.Collection"
	ifaceItem       = "org.freedesktop.Secret.Item"
	ifaceSession    = "org.freedesktop.Secret.Session"
	ifacePrompt     = "org.freedesktop.Secret.Prompt"
	ifaceProps      = "org.freedesktop.DBus.Properties"
	// noPrompt is the path the API returns when no prompt is needed.
	noPrompt = dbus.ObjectPath("/")
)

var (
	errLocked = &dbus.Error{
		Name: "org.freedesktop.Secret.Error.IsLocked",
		Body: []any{"fakesecret refuses every call, as a keyring that stays locked would"},
	}
	errNoSession = &dbus.Error{
		Name: "org.freedesktop.Secret.Error.NoSession",
		Body: []any{"no such session"},
	}
	errNoObject = &dbus.Error{
		Name: "org.freedesktop.Secret.Error.NoSuchObject",
		Body: []any{"no such item"},
	}
)

// Item is a stored item, as a test sees it.
type Item struct {
	Label       string
	Attributes  map[string]string
	Secret      []byte
	ContentType string
}

// secret is the API's Secret struct, signature (oayays).
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// Service is a running fake. Its methods are safe to call from any
// goroutine.
type Service struct {
	conn *dbus.Conn

	mu       sync.Mutex
	items    map[dbus.ObjectPath]*Item
	sessions map[dbus.ObjectPath]bool
	next     int
	refuse   bool
	dismiss  bool
	calls    int
}

// Start connects to the bus in DBUS_SESSION_BUS_ADDRESS, takes the name
// org.freedesktop.secrets, waiting up to NameWait while another connection
// owns it, and serves the API until Close.
func Start() (*Service, error) {
	addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if addr == "" {
		return nil, errors.New("fakesecret: DBUS_SESSION_BUS_ADDRESS isn't set")
	}
	conn, err := dbus.Connect(addr)
	if err != nil {
		return nil, fmt.Errorf("fakesecret: connecting to the session bus: %w", err)
	}
	s := &Service{
		conn:     conn,
		items:    make(map[dbus.ObjectPath]*Item),
		sessions: make(map[dbus.ObjectPath]bool),
	}
	// Export before taking the name, so no call arrives at a missing object.
	exports := []struct {
		v     any
		path  dbus.ObjectPath
		iface string
	}{
		{serviceObj{s}, servicePath, ifaceService},
		{propsObj{s}, servicePath, ifaceProps},
		{collectionObj{s}, loginPath, ifaceCollection},
	}
	for _, e := range exports {
		if err := conn.Export(e.v, e.path, e.iface); err != nil {
			conn.Close()
			return nil, fmt.Errorf("fakesecret: exporting %s: %w", e.iface, err)
		}
	}
	// Without NameFlagReplaceExisting, a request never takes the name from
	// its owner, so a real keyring keeps it and Start fails.
	deadline := time.Now().Add(NameWait)
	for {
		reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("fakesecret: requesting %s: %w", busName, err)
		}
		if reply == dbus.RequestNameReplyPrimaryOwner {
			return s, nil
		}
		if time.Now().After(deadline) {
			conn.Close()
			return nil, fmt.Errorf("fakesecret: another process has owned %s on this bus for %v", busName, NameWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Close gives up the name and disconnects. Releasing the name first, and
// waiting for the bus to confirm it, lets the next Start take it at once.
func (s *Service) Close() error {
	s.conn.ReleaseName(busName)
	return s.conn.Close()
}

// Items returns copies of the stored items, in the order they were made.
func (s *Service) Items() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := slices.SortedFunc(maps.Keys(s.items), byNumber)
	out := make([]Item, 0, len(paths))
	for _, p := range paths {
		it := s.items[p]
		out = append(out, Item{
			Label:       it.Label,
			Attributes:  maps.Clone(it.Attributes),
			Secret:      bytes.Clone(it.Secret),
			ContentType: it.ContentType,
		})
	}
	return out
}

// Refuse makes every later call fail with IsLocked while on is true, like a
// keyring whose unlock prompt the user dismissed.
func (s *Service) Refuse(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse = on
}

// DismissDeletes makes every later Item.Delete ask for a prompt, which the
// user dismisses, while on is true: the item stays, and the prompt's
// Completed signal says it was dismissed. go-keyring v0.2.8 drops that
// flag, so its Delete returns nil all the same.
func (s *Service) DismissDeletes(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dismiss = on
}

// Calls returns how many Secret Service methods have been called.
func (s *Service) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// begin counts a call and takes the lock, which the caller releases. It
// returns IsLocked when the fake is refusing.
func (s *Service) begin() *dbus.Error {
	s.mu.Lock()
	s.calls++
	if s.refuse {
		return errLocked
	}
	return nil
}

// byNumber orders object paths by their trailing number, so items come
// back in the order they were made.
func byNumber(a, b dbus.ObjectPath) int {
	return number(a) - number(b)
}

func number(p dbus.ObjectPath) int {
	s := string(p)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			n, _ := strconv.Atoi(s[i+1:])
			return n
		}
	}
	return 0
}

// The exported objects. godbus exports every method of a value whose last
// result is *dbus.Error, so each D-Bus interface is its own small type.

type serviceObj struct{ s *Service }

func (o serviceObj) OpenSession(algorithm string, _ dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	s := o.s
	err := s.begin()
	defer s.mu.Unlock()
	if err != nil {
		return dbus.MakeVariant(""), noPrompt, err
	}
	if algorithm != "plain" {
		return dbus.MakeVariant(""), noPrompt, &dbus.Error{
			Name: "org.freedesktop.DBus.Error.NotSupported",
			Body: []any{"fakesecret supports only the plain algorithm"},
		}
	}
	s.next++
	path := dbus.ObjectPath(sessionPrefix + strconv.Itoa(s.next))
	if err := s.conn.Export(sessionObj{s, path}, path, ifaceSession); err != nil {
		return dbus.MakeVariant(""), noPrompt, dbus.MakeFailedError(err)
	}
	s.sessions[path] = true
	return dbus.MakeVariant(""), path, nil
}

// Unlock unlocks everything at once, with no prompt.
func (o serviceObj) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s := o.s
	err := s.begin()
	defer s.mu.Unlock()
	if err != nil {
		return []dbus.ObjectPath{}, noPrompt, err
	}
	return objects, noPrompt, nil
}

type propsObj struct{ s *Service }

// Get serves the one property go-keyring reads, the Service's Collections,
// which lists the login collection.
func (o propsObj) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if iface == ifaceService && name == "Collections" {
		return dbus.MakeVariant([]dbus.ObjectPath{loginPath}), nil
	}
	return dbus.MakeVariant(""), &dbus.Error{
		Name: "org.freedesktop.DBus.Error.UnknownProperty",
		Body: []any{iface + "." + name},
	}
}

type collectionObj struct{ s *Service }

func (o collectionObj) SearchItems(attrs map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	s := o.s
	err := s.begin()
	defer s.mu.Unlock()
	if err != nil {
		return []dbus.ObjectPath{}, err
	}
	found := []dbus.ObjectPath{}
	for _, p := range slices.SortedFunc(maps.Keys(s.items), byNumber) {
		if contains(s.items[p].Attributes, attrs) {
			found = append(found, p)
		}
	}
	return found, nil
}

// contains reports whether have has every attribute in want.
func contains(have, want map[string]string) bool {
	for k, v := range want {
		if got, ok := have[k]; !ok || got != v {
			return false
		}
	}
	return true
}

func (o collectionObj) CreateItem(props map[string]dbus.Variant, sec secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	s := o.s
	err := s.begin()
	defer s.mu.Unlock()
	if err != nil {
		return noPrompt, noPrompt, err
	}
	if !s.sessions[sec.Session] {
		return noPrompt, noPrompt, errNoSession
	}
	it := &Item{
		Attributes:  map[string]string{},
		Secret:      bytes.Clone(sec.Value),
		ContentType: sec.ContentType,
	}
	if v, ok := props[ifaceItem+".Label"]; ok {
		it.Label, _ = v.Value().(string)
	}
	if v, ok := props[ifaceItem+".Attributes"]; ok {
		attrs, ok := v.Value().(map[string]string)
		if !ok {
			return noPrompt, noPrompt, &dbus.Error{
				Name: "org.freedesktop.DBus.Error.InvalidArgs",
				Body: []any{"Attributes isn't a{ss}"},
			}
		}
		it.Attributes = maps.Clone(attrs)
	}
	if replace {
		// The API replaces an item with the same attributes.
		for p, old := range s.items {
			if maps.Equal(old.Attributes, it.Attributes) {
				s.items[p] = it
				return p, noPrompt, nil
			}
		}
	}
	s.next++
	path := dbus.ObjectPath(string(loginPath) + "/" + strconv.Itoa(s.next))
	if err := s.conn.Export(itemObj{s, path}, path, ifaceItem); err != nil {
		return noPrompt, noPrompt, dbus.MakeFailedError(err)
	}
	s.items[path] = it
	return path, noPrompt, nil
}

type itemObj struct {
	s    *Service
	path dbus.ObjectPath
}

func (o itemObj) GetSecret(session dbus.ObjectPath) (secret, *dbus.Error) {
	s := o.s
	err := s.begin()
	defer s.mu.Unlock()
	if err != nil {
		return secret{}, err
	}
	if !s.sessions[session] {
		return secret{}, errNoSession
	}
	it, ok := s.items[o.path]
	if !ok {
		return secret{}, errNoObject
	}
	return secret{
		Session:     session,
		Parameters:  []byte{},
		Value:       bytes.Clone(it.Secret),
		ContentType: it.ContentType,
	}, nil
}

func (o itemObj) Delete() (dbus.ObjectPath, *dbus.Error) {
	s := o.s
	err := s.begin()
	defer s.mu.Unlock()
	if err != nil {
		return noPrompt, err
	}
	if _, ok := s.items[o.path]; !ok {
		return noPrompt, errNoObject
	}
	if s.dismiss {
		s.next++
		path := dbus.ObjectPath(promptPrefix + strconv.Itoa(s.next))
		if err := s.conn.Export(promptObj{s, path}, path, ifacePrompt); err != nil {
			return noPrompt, dbus.MakeFailedError(err)
		}
		return path, nil
	}
	delete(s.items, o.path)
	s.conn.Export(nil, o.path, ifaceItem)
	return noPrompt, nil
}

// promptObj is a prompt the user dismisses: it shows nothing, and answers
// Prompt with the Completed signal, dismissed, leaving the item.
type promptObj struct {
	s    *Service
	path dbus.ObjectPath
}

func (o promptObj) Prompt(windowID string) *dbus.Error {
	o.s.mu.Lock()
	o.s.calls++
	o.s.mu.Unlock()
	// go-keyring listens for Completed before it calls Prompt, so the
	// signal can go out before or after Prompt's reply.
	go o.complete()
	return nil
}

func (o promptObj) Dismiss() *dbus.Error {
	go o.complete()
	return nil
}

func (o promptObj) complete() {
	o.s.conn.Emit(o.path, ifacePrompt+".Completed", true, dbus.MakeVariant(""))
	o.s.conn.Export(nil, o.path, ifacePrompt)
}

type sessionObj struct {
	s    *Service
	path dbus.ObjectPath
}

func (o sessionObj) Close() *dbus.Error {
	s := o.s
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, o.path)
	s.conn.Export(nil, o.path, ifaceSession)
	return nil
}
