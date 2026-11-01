package api

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/music/internal/peer"
)

// Device is a Utako that said hello to the server.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
	IP   string `json:"ip,omitempty"`
	// Allowed: the dashboard shares the music with it; Uploads: it lets it
	// upload songs into the library too.
	Allowed   bool  `json:"allowed"`
	Uploads   bool  `json:"uploads"`
	FirstSeen int64 `json:"firstSeen"`
	LastSeen  int64 `json:"lastSeen"`
	// What it's playing lately (not kept).
	Playing   string `json:"-"`
	PlayingAt int64  `json:"-"`
}

// online is how long a device counts as there after its last request
// (Utako says hello every 2 minutes).
const online = 5 * time.Minute

// The most devices kept that aren't allowed: anyone on the network can make
// more.
const maxStrangers = 32

// devices are the devices that said hello, kept in devices.json.
type devices struct {
	file string

	mu    sync.Mutex
	all   map[string]*Device
	dirty bool // last seen times changed since the list was written
}

func openDevices(dir string) (*devices, error) {
	d := &devices{file: filepath.Join(dir, "devices.json"), all: map[string]*Device{}}
	raw, err := os.ReadFile(d.file)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Device
	if err := json.Unmarshal(raw, &list); err != nil {
		log.Printf("music: the devices' list is unreadable (%v): they're allowed again in the dashboard", err)
		return d, nil
	}
	for _, x := range list {
		if x != nil && len(x.ID) == 16 {
			d.all[x.ID] = x
		}
	}
	return d, nil
}

// save writes the list down; the caller holds d.mu.
func (d *devices) save() {
	list := make([]*Device, 0, len(d.all))
	for _, x := range d.all {
		list = append(list, x)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	raw, _ := json.Marshal(list)
	if err := peer.WriteFile(d.file, raw, 0o600); err != nil {
		log.Printf("music: couldn't keep the devices' list: %v", err)
		return
	}
	d.dirty = false
}

// flush writes the list down if devices were seen since.
func (d *devices) flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dirty {
		d.save()
	}
}

// seen notes a request from a device (checked) and returns it as it is now.
func (d *devices) seen(c *peer.Caller, ip string, now time.Time) Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, ok := d.all[c.ID]
	if !ok {
		d.makeRoom()
		x = &Device{ID: c.ID, Key: encodeKey(c.Pub), FirstSeen: now.Unix()}
		d.all[c.ID] = x
		log.Printf("music: a new device, %q (%s): allow it in the dashboard's Music › Devices", c.Name, c.ID)
		defer d.save()
	}
	if c.Name != "" && c.Name != x.Name {
		x.Name = c.Name
		d.dirty = true
	}
	// A ticket may come from another address (a player elsewhere): only a
	// request of its own says where it is.
	if !c.Ticket && ip != "" {
		x.IP = ip
	}
	x.LastSeen = now.Unix()
	d.dirty = true
	return *x
}

// makeRoom forgets the device not allowed that was seen longest ago, when
// there are too many; the caller holds d.mu.
func (d *devices) makeRoom() {
	var oldest *Device
	n := 0
	for _, x := range d.all {
		if x.Allowed {
			continue
		}
		n++
		if oldest == nil || x.LastSeen < oldest.LastSeen {
			oldest = x
		}
	}
	if n >= maxStrangers && oldest != nil {
		delete(d.all, oldest.ID)
	}
}

// playing notes a song a device started.
func (d *devices) playing(id, path string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if x, ok := d.all[id]; ok {
		x.Playing, x.PlayingAt = path, now.Unix()
	}
}

// get is a device, if it's known.
func (d *devices) get(id string) (Device, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, ok := d.all[id]
	if !ok {
		return Device{}, false
	}
	return *x, true
}

// list is every device, the ones seen latest first.
func (d *devices) list() []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Device, 0, len(d.all))
	for _, x := range d.all {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// set changes what a device is allowed.
func (d *devices) set(id string, allowed, uploads *bool) (Device, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, ok := d.all[id]
	if !ok {
		return Device{}, errors.New("no such device")
	}
	if allowed != nil {
		x.Allowed = *allowed
	}
	if uploads != nil {
		x.Uploads = *uploads
	}
	// Uploads go with sharing: a device not let in can't send either.
	if !x.Allowed {
		x.Uploads = false
	}
	d.save()
	return *x, nil
}

// forget removes a device: it shows up again when it next says hello, not
// allowed.
func (d *devices) forget(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.all[id]; !ok {
		return errors.New("no such device")
	}
	delete(d.all, id)
	d.save()
	return nil
}
