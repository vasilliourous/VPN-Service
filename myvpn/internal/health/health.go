package health

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Checker struct {
	probeURL      string
	interval      time.Duration
	fails         int
	maxFails      int
	degraded      bool
	mu            sync.Mutex
	stopCh        chan struct{}
	stopped       chan struct{}
	onDegraded    func()
	onRecovered   func()
	onDead        func()
}

func New(adminHubURL string) *Checker {
	return &Checker{
		probeURL: adminHubURL + "/_/health",
		interval: 15 * time.Second,
		maxFails: 3,
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

func (c *Checker) OnDegraded(fn func())   { c.onDegraded = fn }
func (c *Checker) OnRecovered(fn func())  { c.onRecovered = fn }
func (c *Checker) OnDead(fn func())       { c.onDead = fn }

func (c *Checker) Start() {
	go c.loop()
}

func (c *Checker) Stop() {
	close(c.stopCh)
	<-c.stopped
}

func (c *Checker) IsDegraded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.degraded
}

func (c *Checker) loop() {
	defer close(c.stopped)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.check()
		}
	}
}

func (c *Checker) check() {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(c.probeURL)

	c.mu.Lock()
	defer c.mu.Unlock()

	if err != nil || resp == nil {
		c.fails++
	} else {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			c.fails++
		} else {
			if c.degraded && c.onRecovered != nil {
				c.onRecovered()
			}
			c.fails = 0
			c.degraded = false
			return
		}
	}

	if c.fails >= 1 && !c.degraded {
		fmt.Printf("HEALTH: degraded after %d failures\n", c.fails)
		c.degraded = true
		if c.onDegraded != nil {
			c.onDegraded()
		}
	}

	if c.fails >= c.maxFails {
		fmt.Printf("HEALTH: tunnel dead after %d failures\n", c.fails)
		if c.onDead != nil {
			c.onDead()
		}
	}
}
