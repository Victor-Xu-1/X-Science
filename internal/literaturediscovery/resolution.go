package literaturediscovery

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"time"
)

const resolutionLifetime = 15 * time.Minute
const resolutionLimit = 200

var resolutionTokenValue = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

type resolution struct {
	owner     string
	itemID    string
	revision  int64
	direction string
	confirmed bool
	seed      Record
	expires   time.Time
}

func newResolutionToken() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	random[6] = (random[6] & 0x0f) | 0x40
	random[8] = (random[8] & 0x3f) | 0x80
	text := hex.EncodeToString(random[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}

func (f *Finder) resolutionFor(owner string, request Request) (resolution, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for token, entry := range f.resolutions {
		if !entry.expires.After(now) {
			delete(f.resolutions, token)
		}
	}
	entry, found := f.resolutions[request.ResolutionToken]
	if !found || entry.owner != owner || entry.itemID != request.ItemID ||
		entry.revision != request.ExpectedMetadataRevision || (!entry.confirmed && entry.direction != request.Direction) {
		return resolution{}, false
	}
	entry.seed = cloneRecord(entry.seed)
	return entry, true
}

func (f *Finder) rememberResolution(token string, entry resolution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.resolutions) >= resolutionLimit {
		oldestToken := ""
		var oldest time.Time
		for candidate, value := range f.resolutions {
			if oldestToken == "" || value.expires.Before(oldest) {
				oldestToken, oldest = candidate, value.expires
			}
		}
		delete(f.resolutions, oldestToken)
	}
	entry.seed = cloneRecord(entry.seed)
	f.resolutions[token] = entry
}

func (f *Finder) confirmResolution(token string, resolved resolution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, found := f.resolutions[token]
	if found && entry.owner == resolved.owner && entry.itemID == resolved.itemID &&
		entry.revision == resolved.revision && entry.expires.Equal(resolved.expires) {
		entry.confirmed = true
		f.resolutions[token] = entry
	}
}

func (f *Finder) invalidateReference(owner, itemID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for token, entry := range f.resolutions {
		if entry.owner == owner && entry.itemID == itemID {
			delete(f.resolutions, token)
		}
	}
}
