// Copyright 2026 Abhinav Gautam
// SPDX-License-Identifier: Apache-2.0

package interop

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// image is the reference peer's image, built from ./peer.
const image = "sigma-enip-sim-interop:pycomm3-1.2.16"

// envVar turns the tests on.
const envVar = "SIGMA_ENIP_SIM_INTEROP"

// needDocker skips the test unless the environment variable is 1 and
// Docker answers, and builds the peer image.
func needDocker(t *testing.T) {
	t.Helper()
	if os.Getenv(envVar) != "1" {
		t.Skip("set " + envVar + "=1 to run the interop tests (needs Docker)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker is not running")
	}
	// Built every time (Docker reuses its cached layers), so edits to the
	// peer scripts are never tested against a stale image.
	out, err := exec.Command("docker", "build", "-q", "-t", image, "peer").CombinedOutput()
	if err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
}

// peer is a reference program running in a container: its standard
// input takes commands, its "PEER " output lines are collected.
type peer struct {
	t     *testing.T
	name  string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string

	mu  sync.Mutex
	log []string
}

// startPeer runs `docker run --rm -i --name <unique> <args...>`.
func startPeer(t *testing.T, args ...string) *peer {
	t.Helper()
	p := &peer{t: t, name: fmt.Sprintf("sigma-enip-sim-%d", time.Now().UnixNano()), lines: make(chan string, 1024)}
	full := append([]string{"run", "--rm", "-i", "--name", p.name}, args...)
	p.cmd = exec.Command("docker", full...)
	var err error
	if p.stdin, err = p.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	r, w := io.Pipe()
	p.cmd.Stdout, p.cmd.Stderr = w, w
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			p.mu.Lock()
			p.log = append(p.log, line)
			p.mu.Unlock()
			if strings.HasPrefix(line, "PEER ") {
				p.lines <- strings.TrimPrefix(line, "PEER ")
			}
		}
	}()
	go func() { _ = p.cmd.Wait(); _ = w.Close() }()
	t.Cleanup(func() {
		_ = p.stdin.Close()
		_ = exec.Command("docker", "stop", "-t", "2", p.name).Run()
		if t.Failed() || os.Getenv(envVar+"_LOG") == "1" {
			t.Logf("peer output:\n%s", p.output())
		}
	})
	return p
}

func (p *peer) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.log, "\n")
}

// expect waits for a "PEER " line starting with step (or "exception",
// which fails the test), skipping others, and returns it.
func (p *peer) expect(within time.Duration, step string) string {
	p.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case l := <-p.lines:
			if strings.HasPrefix(l, "exception") {
				p.t.Fatalf("waiting for %q, the peer failed: %s", step, l)
			}
			if l == step || strings.HasPrefix(l, step+" ") {
				return l
			}
		case <-deadline:
			p.t.Fatalf("no peer line %q within %s", step, within)
			return ""
		}
	}
}
