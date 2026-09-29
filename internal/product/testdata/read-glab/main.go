// read-glab is an offline, cross-platform process fixture for the pinned list
// and view argv contract. It has no HTTP or credential-store implementation.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	line := strings.Join(args, " ")
	record, err := os.OpenFile(os.Getenv("GL_AXI_READ_RECORD"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(record, line)
	closeErr := record.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(args) == 1 && args[0] == "version" {
		fmt.Println("glab 1.112.0 (816e3a52)")
		return nil
	}
	if os.Getenv("GITLAB_HOST") != "gitlab.com" {
		return fmt.Errorf("unexpected host")
	}
	response := os.Getenv("GL_AXI_READ_RESPONSE")
	if pages := os.Getenv("GL_AXI_READ_PAGES"); pages != "" {
		page := 0
		for i, arg := range args {
			if arg == "--page" && i+1 < len(args) {
				page, _ = strconv.Atoi(args[i+1])
			}
		}
		expected := os.Getenv("GL_AXI_READ_EXPECTED")
		if expected == "" {
			expected = os.Getenv("GL_AXI_READ_GROUP") + " list --output json --page {page} --per-page 100 -R group/project"
		}
		if page < 1 || page > 10 || line != strings.ReplaceAll(expected, "{page}", strconv.Itoa(page)) {
			return fmt.Errorf("unexpected page selector")
		}
		response = filepath.Join(pages, strconv.Itoa(page)+".json")
	} else if line != os.Getenv("GL_AXI_READ_EXPECTED") {
		return fmt.Errorf("unexpected read argv")
	}
	body, err := os.ReadFile(response)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(body)
	return err
}
