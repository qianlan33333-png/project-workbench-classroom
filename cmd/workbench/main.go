package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aichy126/igo"
	"github.com/aichy126/igo/log"
	"projectworkbench/internal/workbench"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	file := flag.String("config", "config.json", "配置文件")
	hashPassword := flag.Bool("hash-password", false, "从标准输入读取密码并输出 scrypt 哈希")
	flag.Parse()
	if *hashPassword {
		var password string
		if _, e := fmt.Scanln(&password); e != nil {
			return e
		}
		h, e := workbench.PasswordHash(password)
		if e == nil {
			fmt.Println(h)
		}
		return e
	}
	b, e := os.ReadFile(*file)
	if e != nil {
		return e
	}
	var c struct {
		workbench.Config
		Address string
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return e
	}
	if c.DataDir == "" || c.WebDir == "" {
		return fmt.Errorf("DataDir 和 WebDir 必填")
	}
	c.DataDir, e = filepath.Abs(c.DataDir)
	if e != nil {
		return e
	}
	c.WebDir, e = filepath.Abs(c.WebDir)
	if e != nil {
		return e
	}
	if c.Address == "" {
		c.Address = "127.0.0.1:3082"
	}
	if c.Provider == "" {
		c.Provider = "deepseek-official"
	}
	if c.Model == "" {
		c.Model = "deepseek-v4-flash"
	}
	if e = os.MkdirAll(c.DataDir, 0700); e != nil {
		return e
	}
	// igo owns configuration, logging, HTTP lifecycle and the SQLite connection.
	toml := fmt.Sprintf("[local]\naddress = %q\ndebug = false\n[local.logger]\ndir = %q\nname = \"workbench.log\"\naccess = false\nlevel = \"INFO\"\nmax_size = 10\nmax_backups = 3\nmax_age = 7\n[sqlite.workbench]\ndata_source = %q\nmax_open = 4\nmax_idle = 4\n", c.Address, filepath.Join(c.DataDir, "logs"), "file:"+filepath.Join(c.DataDir, "workbench.sqlite")+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=secure_delete(1)&_txlock=immediate")
	igoConfig := filepath.Join(c.DataDir, "igo.toml")
	if e = os.WriteFile(igoConfig, []byte(toml), 0600); e != nil {
		return e
	}
	app, e := igo.NewApp(igoConfig)
	if e != nil {
		return e
	}
	manager := app.DB.Get("workbench")
	if manager == nil {
		return fmt.Errorf("igo SQLite 未初始化")
	}
	db := manager.WriteDB.DB().DB
	store, e := workbench.NewStore(db, c.DataDir)
	if e != nil {
		return e
	}
	store.BackupScrubCommand = c.BackupScrubCommand
	runtime := &workbench.Harness{Config: c.Config, Store: store}
	worker := workbench.NewWorker(store, runtime, runtime.Route)
	recoveryCtx, done := context.WithTimeout(context.Background(), time.Minute)
	e = runtime.CleanupOrphans(recoveryCtx)
	if e == nil {
		e = worker.Recover(recoveryCtx)
	}
	done()
	if e != nil {
		return fmt.Errorf("执行恢复检查未通过: %w", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := &workbench.API{Store: store, Worker: worker, Config: c.Config, Shutdown: ctx.Done()}
	api.Mount(app.Web.Router)
	app.AddStartupHook(func() error { worker.Start(ctx); log.Info("项目工作台已启动"); return nil })
	app.AddShutdownHook(func() error { cancel(); worker.Wait(); return nil })
	return app.RunWithGracefulShutdown()
}
