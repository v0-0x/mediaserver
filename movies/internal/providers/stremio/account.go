package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Importing a Stremio account's addons
//
// The addons installed on a Stremio account are its addon collection, read
// from Stremio's API once signed in (login, then addonCollectionGet).
// Moviko installs the ones it can use, as Store.Install decides for any
// addon, and says why it left the others out. It signs in for that only:
// the password and the session's key are used for those two requests and
// forgotten, never saved nor logged.

// DefaultAPIURL is Stremio's API. $MOVIKO_STREMIO_API_URL overrides it
// (tests, offline demos).
const DefaultAPIURL = "https://api.strem.io"

// APIURL is the address of Stremio's API.
func APIURL() string {
	if u := strings.TrimRight(strings.TrimSpace(os.Getenv("MOVIKO_STREMIO_API_URL")), "/"); u != "" {
		return u
	}
	return DefaultAPIURL
}

// accountHTTP reaches Stremio's API.
var accountHTTP = &http.Client{Timeout: 30 * time.Second}

// AccountAddons signs in to a Stremio account at the API apiURL and returns
// the addons installed on it, in the account's order.
func AccountAddons(ctx context.Context, apiURL, email, password string) ([]Addon, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return nil, errors.New("the Stremio account's email and password are needed")
	}
	var login struct {
		AuthKey string `json:"authKey"`
	}
	if err := callAPI(ctx, apiURL, "login", map[string]any{"type": "Login", "email": email, "password": password}, &login); err != nil {
		return nil, err
	}
	if login.AuthKey == "" {
		return nil, errors.New("Stremio didn't sign in (no session key)")
	}
	var coll struct {
		Addons []json.RawMessage `json:"addons"`
	}
	if err := callAPI(ctx, apiURL, "addonCollectionGet", map[string]any{"type": "AddonCollectionGet", "authKey": login.AuthKey, "update": true}, &coll); err != nil {
		return nil, err
	}
	return parseCollection(coll.Addons), nil
}

// callAPI posts body to the API's method and decodes the "result" of its
// answer into out; an "error" answer becomes the error.
func callAPI(ctx context.Context, apiURL, method string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(apiURL, "/")+"/api/"+method, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := accountHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Stremio: %s", providers.ErrorText(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCollectionSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxCollectionSize {
		return errors.New("Stremio's answer is too large")
	}
	var answer struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		if resp.StatusCode >= 400 {
			return fmt.Errorf("Stremio answered %s", resp.Status)
		}
		return errors.New("Stremio's answer is not valid JSON")
	}
	switch {
	case answer.Error != nil && answer.Error.Message != "":
		return errors.New("Stremio: " + answer.Error.Message)
	case resp.StatusCode >= 400:
		return fmt.Errorf("Stremio answered %s", resp.Status)
	case len(answer.Result) == 0 || string(answer.Result) == "null":
		return errors.New("Stremio's answer is empty")
	}
	return json.Unmarshal(answer.Result, out)
}

// ImportedAddon is an addon of an import.
type ImportedAddon struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TransportURL string `json:"transportUrl"`
	// Reason is why it was left out ("" for those installed).
	Reason string `json:"reason,omitempty"`
}

// ImportResult says which addons were installed and which were left out.
type ImportResult struct {
	Added   []ImportedAddon `json:"added"`
	Skipped []ImportedAddon `json:"skipped"`
}

// Why addons are left out of an import.
const (
	skipInstalled   = "already installed"
	skipOther       = "another configuration of it is installed"
	skipLocal       = "runs on this computer or the local network: install it by its URL to confirm"
	skipAdult       = "flagged as adult content: install it by its URL to confirm"
	skipConfigure   = "needs configuring first"
	skipUnsupported = "no streams or subtitles Moviko can use"
)

// Import installs the addons Moviko can use among addons (a Stremio
// account's), as Install does without any confirmation: those on this
// computer or the LAN, adult ones and those to configure are left out (to
// install by hand, confirming), as are those already installed, in this
// configuration or another (which is kept).
func (s *Store) Import(ctx context.Context, addons []Addon) ImportResult {
	res := ImportResult{Added: []ImportedAddon{}, Skipped: []ImportedAddon{}}
	for _, a := range addons {
		item := ImportedAddon{ID: a.Manifest.ID, Name: a.Manifest.Name, TransportURL: a.TransportURL}
		reason := ""
		for _, x := range s.List() {
			switch {
			case x.TransportURL == a.TransportURL:
				reason = skipInstalled
			case x.Manifest.ID == a.Manifest.ID && reason == "":
				reason = skipOther
			}
		}
		// What the collection says is enough to leave these out.
		switch {
		case reason != "":
		case a.Local:
			reason = skipLocal
		case !a.Supported:
			reason = skipUnsupported
		case a.Manifest.BehaviorHints.Adult:
			reason = skipAdult
		}
		if reason == "" {
			// The addon's own manifest decides (it may have changed since
			// the account got it).
			_, err := s.Install(ctx, a.TransportURL, InstallOptions{})
			var cfg *ConfigurationError
			switch {
			case err == nil:
			case errors.Is(err, ErrLocalAddon):
				reason = skipLocal
			case errors.Is(err, ErrAdultAddon):
				reason = skipAdult
			case errors.As(err, &cfg):
				reason = skipConfigure
			case errors.Is(err, ErrUnsupportedAddon):
				reason = skipUnsupported
			default:
				reason = providers.ErrorText(err)
			}
		}
		if reason != "" {
			item.Reason = reason
			res.Skipped = append(res.Skipped, item)
			continue
		}
		res.Added = append(res.Added, item)
	}
	return res
}
