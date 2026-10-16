package main

import (
	"encoding/json"
	"finicky/browser"
	"finicky/config"
	"finicky/shorturl"
	"fmt"
	"github.com/dop251/goja"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRoutingMatchesStock(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "config.js")
	fixture := `var finickyConfig = {default: {
  defaultBrowser: "Safari", options: {logRequests: false},
  handlers: [
   {match: /work.example.com/, browser: {name: "Google Chrome", profile: "Work", openInBackground: false}},
   {match: /other.example.com/, browser: {name: "Google Chrome", profile: "Other", openInBackground: false}},
   {match: /personal.example.com/, browser: "Firefox"},
   {match: /music.example.com/, browser: "Spotify"}
  ]
 }};`
	if err := os.WriteFile(bundle, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	stock, err := config.New(embeddedFiles, "finickyConfig", bundle)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := config.New(embeddedFiles, "finickyConfig", bundle)
	if err != nil {
		t.Fatal(err)
	}
	urls := []string{
		"https://work.example.com/path?token=TEST_SECRET",
		"https://other.example.com/path", "https://personal.example.com/path",
		"https://music.example.com/path", "https://example.com", "file:///tmp/example.html",
	}

	for i, raw := range urls {
		want, we := evaluateURLStock(stock.Runtime(), raw, nil)
		got, ge := evaluateURL(observed.Runtime(), raw, nil, nil)
		if !reflect.DeepEqual(want, got) || fmt.Sprint(we) != fmt.Sprint(ge) {
			t.Fatalf("case %d differs from stock", i)
		}
	}
}

func evaluateURLStock(vm *goja.Runtime, url string, opener *ProcessInfo) (*browser.BrowserConfig, error) {
	resolvedURL, err := shorturl.ResolveURL(url)
	if err != nil {
		// Continue with original URL if resolution fails
		slog.Info("Failed to resolve short URL", "error", err)

	} else {
		url = resolvedURL
	}

	vm.Set("url", resolvedURL)

	if opener != nil {
		vm.Set("opener", map[string]interface{}{
			"name":     opener.Name,
			"bundleId": opener.BundleID,
			"path":     opener.Path,
		})
		slog.Debug("Setting opener", "name", opener.Name, "bundleId", opener.BundleID, "path", opener.Path)
	} else {
		vm.Set("opener", nil)
		slog.Debug("No opener detected")
	}

	openResult, err := vm.RunString("finickyConfigAPI.openUrl(url, opener, finalConfig)")
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate URL in config: %v", err)
	}

	resultJSON := openResult.ToObject(vm).Export()
	resultBytes, err := json.Marshal(resultJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to process browser configuration: %v", err)
	}

	var browserResult browser.BrowserResult

	if err := json.Unmarshal(resultBytes, &browserResult); err != nil {
		return nil, fmt.Errorf("failed to parse browser configuration: %v", err)
	}

	slog.Debug("Final browser options",
		"name", browserResult.Browser.Name,
		"openInBackground", browserResult.Browser.OpenInBackground,
		"profile", browserResult.Browser.Profile,
		"args", browserResult.Browser.Args,
		"appType", browserResult.Browser.AppType,
	)
	var resultErr error
	if browserResult.Error != "" {
		resultErr = fmt.Errorf("%s", browserResult.Error)
	}
	return &browserResult.Browser, resultErr
}
