// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"github.com/globocom/tsuru/app/bind"
	"github.com/globocom/tsuru/db"
	"github.com/globocom/tsuru/log"
	"github.com/globocom/tsuru/queue"
	"github.com/globocom/tsuru/service"
	"labix.org/v2/mgo/bson"
	. "launchpad.net/gocheck"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"strings"
)

func (s *S) TestHandleMessage(c *C) {
	s.provisioner.PrepareOutput([]byte("exported"))
	a := App{
		Name: "nemesis",
		Units: []Unit{
			{
				Name:    "i-00800",
				State:   "started",
				Machine: 19,
			},
		},
		Env: map[string]bind.EnvVar{
			"http_proxy": {
				Name:   "http_proxy",
				Value:  "http://myproxy.com:3128/",
				Public: true,
			},
		},
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	msg := queue.Message{Action: regenerateApprc, Args: []string{a.Name}}
	handle(&msg)
	cmds := s.provisioner.GetCmds("", &a)
	c.Assert(cmds, HasLen, 1)
	output := strings.Replace(cmds[0].Cmd, "\n", " ", -1)
	outputRegexp := `^cat > /home/application/apprc <<END # generated by tsuru.*`
	outputRegexp += `export http_proxy="http://myproxy.com:3128/" END $`
	c.Assert(output, Matches, outputRegexp)
}

func (s *S) TestHandleMessageWithSpecificUnit(c *C) {
	s.provisioner.PrepareOutput([]byte("exported"))
	a := App{
		Name: "nemesis",
		Units: []Unit{
			{
				Name:    "nemesis/0",
				State:   "started",
				Machine: 19,
			},
			{
				Name:    "nemesis/1",
				State:   "started",
				Machine: 20,
			},
			{
				Name:    "nemesis/2",
				State:   "started",
				Machine: 23,
			},
		},
		Env: map[string]bind.EnvVar{
			"http_proxy": {
				Name:   "http_proxy",
				Value:  "http://myproxy.com:3128/",
				Public: true,
			},
		},
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	msg := queue.Message{Action: regenerateApprc, Args: []string{a.Name, "nemesis/1"}}
	handle(&msg)
	cmds := s.provisioner.GetCmds("", &a)
	c.Assert(cmds, HasLen, 1)
	output := strings.Replace(cmds[0].Cmd, "\n", " ", -1)
	outputRegexp := `^cat > /home/application/apprc <<END # generated by tsuru.*`
	outputRegexp += `export http_proxy="http://myproxy.com:3128/" END $`
	c.Assert(output, Matches, outputRegexp)
}

// TODO(fss): fix this test (app state related).
func (s *S) TestHandleMessageErrors(c *C) {
	c.Skip("Outdated test.")
	var data = []struct {
		action      string
		args        []string
		unitName    string
		expectedLog string
	}{
		{
			action:      "unknown-action",
			args:        []string{"does not matter"},
			expectedLog: `Error handling "unknown-action": invalid action.`,
		},
		{
			action: startApp,
			args:   []string{"nemesis"},
			expectedLog: `Error handling "start-app" for the app "nemesis":` +
				` The status of the app and all units should be "started" (the app is "pending").`,
		},
		{
			action: startApp,
			args:   []string{"totem", "totem/0", "totem/1"},
			expectedLog: `Error handling "start-app" for the app "totem":` +
				` The status of the app and all units should be "started" (the app is "started").`,
		},
		{
			action: regenerateApprc,
			args:   []string{"nemesis"},
			expectedLog: `Error handling "regenerate-apprc" for the app "nemesis":` +
				` The status of the app and all units should be "started" (the app is "pending").`,
		},
		{
			action: regenerateApprc,
			args:   []string{"totem", "totem/0", "totem/1"},
			expectedLog: `Error handling "regenerate-apprc" for the app "totem":` +
				` The status of the app and all units should be "started" (the app is "started").`,
		},
		{
			action:      regenerateApprc,
			args:        []string{"unknown-app"},
			expectedLog: `Error handling "regenerate-apprc": app "unknown-app" does not exist.`,
		},
		{
			action:      regenerateApprc,
			expectedLog: `Error handling "regenerate-apprc": this action requires at least 1 argument.`,
		},
		{
			action: regenerateApprc,
			args:   []string{"marathon"},
			expectedLog: `Error handling "regenerate-apprc" for the app "marathon":` +
				` the app is in "error" state.`,
		},
		{
			action: regenerateApprc,
			args:   []string{"territories"},
			expectedLog: `Error handling "regenerate-apprc" for the app "territories":` +
				` the app is down.`,
		},
	}
	var buf bytes.Buffer
	a := App{Name: "nemesis"}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	a = App{
		Name: "totem",
		Units: []Unit{
			{Name: "totem/0", State: "pending"},
			{Name: "totem/1", State: "started"},
		},
	}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	a = App{Name: "marathon"}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	a = App{Name: "territories"}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	log.SetLogger(stdlog.New(&buf, "", 0))
	for _, d := range data {
		message := queue.Message{Action: d.action}
		if len(d.args) > 0 {
			message.Args = d.args
		}
		handle(&message)
		defer message.Delete() // Sanity
	}
	content := buf.String()
	lines := strings.Split(content, "\n")
	for i, d := range data {
		var found bool
		for j := i; j < len(lines); j++ {
			if lines[j] == d.expectedLog {
				found = true
				break
			}
		}
		if !found {
			c.Errorf("\nWant: %q.\nGot:\n%s", d.expectedLog, content)
		}
	}
}

func (s *S) TestHandleRestartAppMessage(c *C) {
	s.provisioner.PrepareOutput([]byte("started"))
	a := App{
		Name: "nemesis",
		Units: []Unit{
			{
				Name:    "i-00800",
				State:   "started",
				Machine: 19,
			},
		},
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	message := queue.Message{Action: startApp, Args: []string{a.Name}}
	handle(&message)
	cmds := s.provisioner.GetCmds("/var/lib/tsuru/hooks/restart", &a)
	c.Assert(cmds, HasLen, 1)
}

func (s *S) TestHandleRegenerateAndRestart(c *C) {
	s.provisioner.PrepareOutput([]byte("exported"))
	s.provisioner.PrepareOutput([]byte("started"))
	a := App{
		Name: "nemesis",
		Units: []Unit{
			{
				Name:    "i-00800",
				State:   "started",
				Machine: 19,
			},
		},
		Env: map[string]bind.EnvVar{
			"http_proxy": {
				Name:   "http_proxy",
				Value:  "http://myproxy.com:3128/",
				Public: true,
			},
		},
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	msg := queue.Message{Action: RegenerateApprcAndStart, Args: []string{a.Name}}
	handle(&msg)
	cmds := s.provisioner.GetCmds("", &a)
	c.Assert(cmds, HasLen, 3)
	output := strings.Replace(cmds[0].Cmd, "\n", " ", -1)
	outputRegexp := `^cat > /home/application/apprc <<END # generated by tsuru.*`
	outputRegexp += `export http_proxy="http://myproxy.com:3128/" END $`
	c.Assert(output, Matches, outputRegexp)
	c.Assert(cmds[1].Cmd, Equals, "cat /home/application/current/app.conf")
	c.Assert(cmds[2].Cmd, Equals, "/var/lib/tsuru/hooks/restart")
}

func (s *S) TestUnitListStarted(c *C) {
	var tests = []struct {
		input    []Unit
		expected bool
	}{
		{
			[]Unit{
				{State: "started"},
				{State: "started"},
				{State: "started"},
			},
			true,
		},
		{nil, true},
		{
			[]Unit{
				{State: "started"},
				{State: "blabla"},
			},
			false,
		},
	}
	for _, t := range tests {
		l := unitList(t.input)
		if got := l.Started(); got != t.expected {
			c.Errorf("l.Started(): want %v. Got %v.", t.expected, got)
		}
	}
}

func (s *S) TestEnqueueUsesInternalQueue(c *C) {
	enqueue(queue.Message{Action: "do-something"})
	_, err := queue.Get("default", 1e6)
	c.Assert(err, NotNil)
	msg, err := queue.Get(queueName, 1e6)
	c.Assert(err, IsNil)
	c.Assert(msg.Action, Equals, "do-something")
	msg.Delete()
}

func (s *S) TestHandlerListenToSpecificQueue(c *C) {
	c.Assert(handler.Queues, DeepEquals, []string{queueName, QueueName})
}

func (s *S) TestHandleBindServiceMessage(c *C) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"DATABASE_USER":"root","DATABASE_PASSWORD":"s3cr3t"}`))
	}))
	defer ts.Close()
	srvc := service.Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := srvc.Create()
	c.Assert(err, IsNil)
	defer db.Session.Services().Remove(bson.M{"_id": "mysql"})
	instance := service.ServiceInstance{Name: "my-mysql", ServiceName: "mysql", Teams: []string{s.team.Name}}
	instance.Create()
	defer db.Session.ServiceInstances().Remove(bson.M{"_id": "my-mysql"})
	a := App{
		Name: "nemesis",
		Units: []Unit{
			{
				Name:    "i-00800",
				State:   "started",
				Machine: 19,
			},
		},
	}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	err = instance.AddApp(a.Name)
	c.Assert(err, IsNil)
	err = db.Session.ServiceInstances().Update(bson.M{"name": instance.Name}, instance)
	c.Assert(err, IsNil)
	message := queue.Message{Action: bindService, Args: []string{a.Name, a.Units[0].Name}}
	handle(&message)
	c.Assert(called, Equals, true)
}
