// Command vigia watches the LibreEcho voice stack on the Echo, records every
// failure in an event log and alerts the owner's phone through ntfy.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// processes that must always run (substring of /proc/<pid>/cmdline argv[0]).
var processes = map[string]string{
	"libreecho-agentd": "el asistente (agentd)",
	"libreecho-sttd":   "el reconocimiento de voz (sttd)",
	"libreecho-ttsd":   "la voz (ttsd)",
}

func procProblems() []Problem {
	found := map[string]bool{}
	dirs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, d := range dirs {
		b, err := os.ReadFile(d)
		if err != nil || len(b) == 0 {
			continue
		}
		argv0 := filepath.Base(strings.SplitN(string(b), "\x00", 2)[0])
		for name := range processes {
			if strings.HasPrefix(argv0, name) {
				found[name] = true
			}
		}
	}
	var out []Problem
	for name, label := range processes {
		if !found[name] {
			out = append(out, Problem{Key: "proc:" + name, Text: "No está corriendo " + label + "."})
		}
	}
	return out
}

func httpProblem(client *http.Client, url, key, text string) []Problem {
	resp, err := client.Get(url)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			return nil
		}
		err = fmt.Errorf("status %s", resp.Status)
	}
	return []Problem{{Key: key, Text: text + " (" + err.Error() + ")."}}
}

func tcpProblem(addr, key, text string) []Problem {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return []Problem{{Key: key, Text: text}}
	}
	c.Close()
	return nil
}

func diskProblem(path string, minFree float64) []Problem {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return nil
	}
	free := float64(st.Bavail) / float64(st.Blocks)
	if free < minFree {
		return []Problem{{Key: "disk:" + path, Text: fmt.Sprintf("Queda poco espacio en %s (%.0f %% libre).", path, free*100)}}
	}
	return nil
}

func main() {
	conf := flag.String("conf", "/data/libreecho/local/vigia.conf", "config file with topic= and optional server=")
	interval := flag.Duration("interval", 30*time.Second, "time between checks")
	recheck := flag.Duration("recheck", 90*time.Second, "wait this long before alerting a lasting failure")
	rate := flag.Duration("rate", time.Hour, "alert the same failure at most once per this window")
	statusFile := flag.String("status", "/run/libreecho/local.status", "local supervisor status file")
	runDir := flag.String("run-dir", "/run/libreecho-local", "local supervisor run directory")
	logFile := flag.String("log", "/var/log/libreecho/local/puente.log", "bridge log to scan for errors")
	events := flag.String("events", "/data/libreecho/local/vigia/events.jsonl", "event history file")
	health := flag.String("health", "http://127.0.0.1:18080/health", "bridge health URL")
	wyoming := flag.String("wyoming", "127.0.0.1:10200", "cloud voice Wyoming address")
	testAlert := flag.String("test-alert", "", "send this text as a notification and exit")
	once := flag.Bool("once", false, "run the checks once, print the problems and exit (1 if any)")
	flag.Parse()

	client := &http.Client{Timeout: 10 * time.Second}
	var notifier *ntfy
	if c, err := loadConfig(*conf); err != nil {
		log.Printf("no config (%v): failures are only recorded", err)
	} else if c["topic"] != "" {
		server := c["server"]
		if server == "" {
			server = "https://ntfy.sh"
		}
		notifier = &ntfy{Server: server, Topic: c["topic"], HTTP: client}
	}

	if *testAlert != "" {
		if notifier == nil {
			log.Fatal("no topic configured")
		}
		if err := notifier.Send(Alert{Key: "test", Message: *testAlert}); err != nil {
			log.Fatal(err)
		}
		fmt.Println("sent")
		return
	}

	restarts := &restartWatch{Dir: *runDir}
	scanner := &logScanner{Path: *logFile}
	check := func() []Problem {
		var p []Problem
		if b, err := os.ReadFile(*statusFile); err == nil {
			p = append(p, statusProblems(parseStatus(string(b)))...)
		} else {
			p = append(p, Problem{Key: "local:status", Text: "No puedo leer el estado de los servicios locales."})
		}
		p = append(p, restarts.Check()...)
		p = append(p, procProblems()...)
		p = append(p, httpProblem(client, *health, "http:puente", "El puente no responde")...)
		p = append(p, tcpProblem(*wyoming, "tcp:wyoming", "La voz de la nube no responde en "+*wyoming+".")...)
		p = append(p, diskProblem("/data", 0.10)...)
		for _, line := range scanner.Scan() {
			if pr, ok := logProblem(line); ok {
				p = append(p, pr)
			}
		}
		return p
	}

	if *once {
		p := check()
		for _, pr := range p {
			fmt.Printf("%s\t%s\n", pr.Key, pr.Text)
		}
		if len(p) > 0 {
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	evlog := &eventLog{Path: *events, Max: 256 << 10}
	w := newWatcher(*recheck, *rate)
	w.OnEvent = evlog.Add
	log.Printf("vigia watching every %s (alerts: %v)", *interval, notifier != nil)
	for {
		for _, a := range w.Tick(time.Now(), check()) {
			log.Printf("alert %s: %s", a.Key, a.Message)
			if notifier == nil {
				continue
			}
			if err := notifier.Send(a); err != nil {
				log.Printf("ntfy: %v", err)
				evlog.Add(Event{Time: time.Now(), Key: a.Key, Status: "send-failed", Detail: err.Error()})
			}
		}
		time.Sleep(*interval)
	}
}
