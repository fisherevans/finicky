package browser

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestLaunchArgumentsMatchStock(t *testing.T) {
	t.Setenv("FINICKY_CHROME_HANDOFF", "0")
	dir := t.TempDir()
	capture := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FINICKY_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "open"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("FINICKY_CAPTURE", capture)
	foreground, background := false, true
	cases := []BrowserConfig{
		{Name: "Google Chrome", Profile: "Default", OpenInBackground: &foreground},
		{Name: "Google Chrome", Profile: "Profile 1", OpenInBackground: &foreground},
		{Name: "Google Chrome", Profile: "Default"},
		{Name: "Firefox", OpenInBackground: &foreground},
		{Name: "us.zoom.xos", AppType: "bundleId"},
		{Name: "Spotify"},
		{Name: "Google Chrome", OpenInBackground: &background},
		{Name: "Google Chrome", Args: []string{"--new-window", "https://example.com"}},
		{Name: "Google Chrome", Profile: "MissingProfile"},
	}
	for i, c := range cases {
		c.URL = "https://example.com/path?token=TEST_SECRET"
		if err := launchStock(c, false, true); err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		if err := LaunchBrowser(c, false, true, nil); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != string(got) {
			t.Fatalf("case %d arguments changed", i)
		}
	}
}

func launchStock(config BrowserConfig, dryRun bool, openInBackgroundByDefault bool) error {
	if config.AppType == "none" {
		slog.Info("AppType is 'none', not launching any browser")
		return nil
	}

	slog.Info("Starting browser", "name", config.Name, "url", config.URL)

	var openArgs []string

	if config.AppType == "bundleId" {
		openArgs = []string{"-b", config.Name}
	} else {
		openArgs = []string{"-a", config.Name}
	}

	var openInBackground bool = openInBackgroundByDefault

	if config.OpenInBackground != nil {
		openInBackground = *config.OpenInBackground
	}

	if openInBackground {
		openArgs = append(openArgs, "-g")
	}

	// Handle profile and custom args
	profileArgument, ok := resolveBrowserProfileArgument(config.Name, config.Profile)
	hasCustomArgs := len(config.Args) > 0

	// Add -n flag if profile is used (required for profile switching)
	if ok {
		openArgs = append(openArgs, "-n")
	}

	// Add --args if we have profile args or custom args
	if ok || hasCustomArgs {
		if !slices.Contains(config.Args, "--args") {
			openArgs = append(openArgs, "--args")
		}
		// Add profile argument first if present
		if ok {
			openArgs = append(openArgs, profileArgument)
		}

		// Add custom args or URL
		if hasCustomArgs {
			openArgs = append(openArgs, config.Args...)
		} else {
			openArgs = append(openArgs, config.URL)
		}
	} else {
		// No special args, just add the URL
		openArgs = append(openArgs, config.URL)
	}

	cmd := exec.Command("open", openArgs...)

	// Pretty print the command with proper escaping
	prettyCmd := formatCommand(cmd.Path, cmd.Args)

	if dryRun {
		slog.Debug("Would run command (dry run)", "command", prettyCmd)
		return nil
	} else {
		slog.Debug("Run command", "command", prettyCmd)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		return fmt.Errorf("error reading stderr: %v", err)
	}

	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		return fmt.Errorf("error reading stdout: %v", err)
	}

	cmdErr := cmd.Wait()

	if len(stderrBytes) > 0 {
		slog.Error("Command returned error", "error", string(stderrBytes))
	}
	if len(stdoutBytes) > 0 {
		slog.Debug("Command returned output", "output", string(stdoutBytes))
	}

	if cmdErr != nil {
		return fmt.Errorf("command failed: %v", cmdErr)
	}

	return nil
}
