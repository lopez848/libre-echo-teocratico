package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// plannedFile is read by vigia: services listed there with an unexpired
// time are restarting on purpose, so their failures are not alerted.
const plannedFile = "/data/libreecho/local/vigia/planned"

// announceRestart records "service unix-expiry" in the planned file,
// keeping other unexpired entries and dropping expired ones.
func announceRestart(path, service string, now time.Time, d time.Duration) error {
	var keep []string
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) != 2 || f[0] == service {
				continue
			}
			if until, err := strconv.ParseInt(f[1], 10, 64); err == nil && now.Before(time.Unix(until, 0)) {
				keep = append(keep, line)
			}
		}
	}
	keep = append(keep, fmt.Sprintf("%s %d", service, now.Add(d).Unix()))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(keep, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
