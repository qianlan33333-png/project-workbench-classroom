package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/types/known/durationpb"
	"workbench-native/internal/api"
	"workbench-native/internal/core"
	"workbench-native/internal/engine"
)

type modelBridge struct {
	URL, Token string
	Client     *http.Client
}

func (m modelBridge) hostCall(ctx context.Context, path string, input, output any) error {
	b, e := json.Marshal(input)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", m.URL+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	res, e := m.Client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		switch res.StatusCode {
		case http.StatusBadRequest:
			return core.ErrInvalid
		case http.StatusUnauthorized, http.StatusForbidden:
			return core.ErrDenied
		case http.StatusConflict:
			return core.ErrConflict
		}
		return fmt.Errorf("Harness host returned %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2*1024*1024)).Decode(output)
}
func (m modelBridge) OwnerRequest(ctx context.Context, p core.Principal) (engine.OwnerRequest, error) {
	var v engine.OwnerRequest
	e := m.hostCall(ctx, "/owner-request", p, &v)
	return v, e
}
func (m modelBridge) Attachments(ctx context.Context, p core.Principal, id string) ([]engine.Attachment, error) {
	var v []engine.Attachment
	e := m.hostCall(ctx, "/attachments", map[string]any{"principal": p, "attachmentId": id}, &v)
	return v, e
}
func (m modelBridge) Dispatch(ctx context.Context, r core.Run) error {
	return m.hostCall(ctx, "/dispatch", r, &map[string]any{})
}

func (m modelBridge) Generate(ctx context.Context, input engine.ModelRequest) (engine.ModelResult, error) {
	b, e := json.Marshal(input)
	if e != nil {
		return engine.ModelResult{}, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", m.URL+"/model", bytes.NewReader(b))
	if e != nil {
		return engine.ModelResult{}, e
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, e := m.Client.Do(req)
	if e != nil {
		return engine.ModelResult{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return engine.ModelResult{}, fmt.Errorf("model bridge returned %d", resp.StatusCode)
	}
	var out engine.ModelResult
	e = json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&out)
	if e == nil && out.Text == "" {
		e = errors.New("empty model result")
	}
	return out, e
}
func main() {
	if e := run(); e != nil {
		slog.Error("native worker stopped", "error", e)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Getenv("WORKBENCH_HOST_TOKEN")) < 32 {
		return errors.New("WORKBENCH_HOST_TOKEN must contain at least 32 characters")
	}
	s, e := core.Open(ctx, os.Getenv("WORKBENCH_MYSQL_DSN"))
	if e != nil {
		return e
	}
	defer s.DB.Close()
	if e = s.Migrate(ctx); e != nil {
		return e
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return nil
	}
	c, e := client.Dial(client.Options{HostPort: env("TEMPORAL_ADDRESS", "127.0.0.1:7233"), Namespace: env("TEMPORAL_NAMESPACE", "workbench")})
	if e != nil {
		return e
	}
	defer c.Close()
	if len(os.Args) > 1 && os.Args[1] == "init-temporal" {
		_, e = c.WorkflowService().RegisterNamespace(ctx, &workflowservice.RegisterNamespaceRequest{Namespace: env("TEMPORAL_NAMESPACE", "workbench"), WorkflowExecutionRetentionPeriod: durationpb.New(7 * 24 * time.Hour)})
		var exists *serviceerror.NamespaceAlreadyExists
		if errors.As(e, &exists) {
			return nil
		}
		return e
	}
	bridge := modelBridge{URL: env("HARNESS_BRIDGE_URL", "http://127.0.0.1:7382"), Token: os.Getenv("WORKBENCH_HOST_TOKEN"), Client: &http.Client{Timeout: 150 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	a := &engine.Activities{Store: s, Model: bridge, Host: bridge, Temporal: c}
	address := env("WORKBENCH_LISTEN", "127.0.0.1:7381")
	host, _, e := net.SplitHostPort(address)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("worker HTTP must bind loopback")
	}
	var backgroundReady atomic.Bool
	apiServer := &api.Server{Store: s, Temporal: c, Token: os.Getenv("WORKBENCH_HOST_TOKEN"), Host: bridge, BackgroundReady: backgroundReady.Load}
	server := &http.Server{Addr: address, Handler: apiServer.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	listener, e := net.Listen("tcp", address)
	if e != nil {
		return e
	}
	go func() { errCh <- server.Serve(listener) }()
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = server.Shutdown(stop)
	}()
	if e = waitForDeployment(ctx, env("WORKBENCH_DEPLOYMENT_MARKER", "/opt/project-workbench/.deploying"), errCh); e != nil {
		if errors.Is(e, context.Canceled) {
			return nil
		}
		return e
	}

	bw := worker.New(c, engine.BatchQueue, worker.Options{MaxConcurrentActivityExecutionSize: 4, WorkerStopTimeout: 30 * time.Second})
	bw.RegisterWorkflow(engine.Batch)
	bw.RegisterWorkflow(engine.Sync)
	bw.RegisterWorkflow(engine.WriteTask)
	bw.RegisterWorkflow(engine.Program)
	bw.RegisterWorkflow(engine.ScheduledProgram)
	bw.RegisterActivity(a)
	if e = bw.Start(); e != nil {
		return e
	}
	defer bw.Stop()
	iw := worker.New(c, engine.InteractiveQueue, worker.Options{MaxConcurrentActivityExecutionSize: 2, WorkerStopTimeout: 40 * time.Second})
	iw.RegisterWorkflow(engine.Followup)
	iw.RegisterWorkflow(engine.Daily)
	iw.RegisterWorkflow(engine.Dispatch)
	iw.RegisterWorkflow(engine.SendMessage)
	iw.RegisterWorkflow(engine.WaitAndSend)
	iw.RegisterWorkflow(engine.ReceiptOnly)
	iw.RegisterWorkflow(engine.NotificationSweep)
	iw.RegisterWorkflow(engine.DispatchBatch)
	iw.RegisterWorkflow(engine.SubmitMessage)
	iw.RegisterWorkflow(engine.ReceiptSweep)
	iw.RegisterWorkflow(engine.AutomaticReceipt)
	iw.RegisterActivity(a)
	if e = iw.Start(); e != nil {
		return e
	}
	defer iw.Stop()
	_, e = c.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: "owner-notifications", Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Minute}}}, Action: &client.ScheduleWorkflowAction{ID: "owner-notification-sweep", Workflow: engine.NotificationSweep, TaskQueue: engine.InteractiveQueue}, Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP})
	var scheduleExists *serviceerror.AlreadyExists
	if e != nil && !errors.As(e, &scheduleExists) && !errors.Is(e, temporal.ErrScheduleAlreadyRunning) {
		return e
	}
	_, e = c.ScheduleClient().Create(ctx, client.ScheduleOptions{ID: "message-receipts", Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: time.Minute}}}, Action: &client.ScheduleWorkflowAction{ID: "receipt-sweep", Workflow: engine.ReceiptSweep, TaskQueue: engine.InteractiveQueue}, Overlap: enums.SCHEDULE_OVERLAP_POLICY_SKIP})
	if e != nil && !errors.As(e, &scheduleExists) && !errors.Is(e, temporal.ErrScheduleAlreadyRunning) {
		return e
	}

	backgroundReady.Store(true)
	go apiServer.DeliverSignals(ctx)
	select {
	case <-ctx.Done():
		return nil
	case e = <-errCh:
		if e == http.ErrServerClosed {
			return nil
		}
		return e
	}
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
