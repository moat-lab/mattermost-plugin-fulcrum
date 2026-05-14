// pluginctl is the minimal deploy helper used by `make deploy`. It uploads a
// bundle to a Mattermost server and enables it. Auth comes from the
// MM_ADMIN_TOKEN env var; the server URL from MM_SERVICESETTINGS_SITEURL.
//
// Usage:
//
//	pluginctl deploy <plugin-id> <bundle.tar.gz>
//	pluginctl enable  <plugin-id>
//	pluginctl disable <plugin-id>
//
// This is a thin wrapper around the Mattermost REST API; it does not embed
// the mattermost-server client library because we only need three calls.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "deploy":
		if len(args) != 3 {
			usage()
			os.Exit(2)
		}
		if err := deploy(args[1], args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "enable":
		if len(args) != 2 {
			usage()
			os.Exit(2)
		}
		if err := setEnable(args[1], true); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "disable":
		if len(args) != 2 {
			usage()
			os.Exit(2)
		}
		if err := setEnable(args[1], false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: pluginctl deploy <id> <bundle.tar.gz>")
	fmt.Fprintln(os.Stderr, "       pluginctl enable  <id>")
	fmt.Fprintln(os.Stderr, "       pluginctl disable <id>")
	fmt.Fprintln(os.Stderr, "env:   MM_SERVICESETTINGS_SITEURL=https://... MM_ADMIN_TOKEN=<token>")
}

func config() (string, string, error) {
	url := strings.TrimRight(os.Getenv("MM_SERVICESETTINGS_SITEURL"), "/")
	tok := os.Getenv("MM_ADMIN_TOKEN")
	if url == "" || tok == "" {
		return "", "", fmt.Errorf("MM_SERVICESETTINGS_SITEURL and MM_ADMIN_TOKEN must be set")
	}
	return url, tok, nil
}

func deploy(id, bundle string) error {
	url, tok, err := config()
	if err != nil {
		return err
	}
	f, err := os.Open(bundle)
	if err != nil {
		return fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()

	// POST /api/v4/plugins?force=true (force replaces any existing version).
	req, err := http.NewRequest(http.MethodPost, url+"/api/v4/plugins?force=true", f)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/octet-stream")
	if err := do(req); err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	return setEnable(id, true)
}

func setEnable(id string, on bool) error {
	url, tok, err := config()
	if err != nil {
		return err
	}
	verb := "enable"
	if !on {
		verb = "disable"
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v4/plugins/%s/%s", url, id, verb), bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return do(req)
}

func do(req *http.Request) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
