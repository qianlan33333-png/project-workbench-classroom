//go:build darwin || windows

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "课堂工作台启动失败：", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bundle := filepath.Dir(exe)
	if err = os.Chdir(bundle); err != nil {
		return err
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	state := filepath.Join(base, "WorkbenchClassroom")
	if err = os.MkdirAll(state, 0700); err != nil {
		return err
	}
	// A live file lock prevents two copies opening the same MySQL data.
	lock, err := lockState(filepath.Join(state, "running.lock"))
	if err != nil {
		return fmt.Errorf("请先关闭正在运行的课堂工作台：%w", err)
	}
	defer lock()
	logDir := filepath.Join(state, "logs")
	if err = os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	fmt.Println("课堂数据目录", state)
	fmt.Println("启动日志目录", logDir)
	var started []*child
	defer func() {
		for i := len(started) - 1; i >= 0; i-- {
			started[i].close()
		}
	}()
	start := func(name, exe string, args []string, env []string, stdin io.Reader, scan chan<- string) (*child, error) {
		c, e := startChild(ctx, logDir, name, exe, args, env, stdin, scan)
		if e == nil {
			started = append(started, c)
		}
		return c, e
	}
	config, err := os.ReadFile(filepath.Join(bundle, "activation.json"))
	if err != nil {
		return err
	}
	var activation struct {
		Issuer string `json:"issuer"`
	}
	if err = json.Unmarshal(config, &activation); err != nil {
		return err
	}
	issuer, err := url.Parse(activation.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Hostname() == "" || issuer.String() != "https://"+issuer.Host {
		return errors.New("安装包缺少有效的 HTTPS 发码地址")
	}
	pub := filepath.Join(bundle, "activation-public.pem")
	if _, err = os.Stat(pub); err != nil {
		return err
	}

	ports, err := instancePorts(state)
	if err != nil {
		return err
	}
	dbAddr, temporalAddr, workerAddr, bridgeAddr, webAddr := ports[0], ports[1], ports[2], ports[3], ports[4]
	password, err := instancePassword(state, "local-password")
	if err != nil {
		return err
	}
	hostToken, err := instancePassword(state, "host-token")
	if err != nil {
		return err
	}
	mysqlDir := filepath.Join(bundle, "mysql")
	datadir := filepath.Join(state, "mysql")
	mysqld := executable(mysqlDir, "bin", "mysqld")
	baseArgs := []string{"--no-defaults", "--basedir=" + mysqlDir, "--datadir=" + datadir}
	if _, err = os.Stat(filepath.Join(datadir, "mysql")); errors.Is(err, os.ErrNotExist) {
		fmt.Println("正在初始化本机数据库")
		// --init-file below sets the password before clients are accepted.
		if err = command(ctx, logDir, "mysql-init", mysqld, append(baseArgs, "--initialize-insecure"), nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	initPath := filepath.Join(state, "mysql-init.sql")
	sql := fmt.Sprintf("ALTER USER 'root'@'localhost' IDENTIFIED BY '%s';\n", password)
	for _, db := range []string{"workbench", "temporal", "temporal_visibility"} {
		sql += "CREATE DATABASE IF NOT EXISTS " + db + " CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;\n"
	}
	sql += fmt.Sprintf("CREATE USER IF NOT EXISTS 'classroom'@'127.0.0.1' IDENTIFIED BY '%s';\n", password)
	for _, db := range []string{"workbench", "temporal", "temporal_visibility"} {
		sql += "GRANT ALL ON " + db + ".* TO 'classroom'@'127.0.0.1';\n"
	}
	if err = os.WriteFile(initPath, []byte(sql), 0600); err != nil {
		return err
	}
	defer os.Remove(initPath)
	mysqlArgs := append(baseArgs, "--init-file="+initPath, "--bind-address=127.0.0.1", "--port="+port(dbAddr), "--mysqlx=0", "--skip-log-bin", "--innodb-buffer-pool-size=64M", "--pid-file="+filepath.Join(state, "mysql.pid"))
	if runtime.GOOS != "windows" {
		mysqlArgs = append(mysqlArgs, "--socket="+filepath.Join(os.TempDir(), "wb-"+password[:12]+".sock"))
	}
	db, err := start("mysql", mysqld, mysqlArgs, nil, nil, nil)
	if err != nil {
		return err
	}
	dsn := mysql.NewConfig()
	dsn.User = "classroom"
	dsn.Passwd = password
	dsn.Net = "tcp"
	dsn.Addr = dbAddr
	dsn.DBName = "workbench"
	dsn.ParseTime = true
	if err = waitTCP(ctx, db, dbAddr, 60*time.Second); err != nil {
		return err
	}
	os.Remove(initPath)
	// Prefer a clean shutdown when the console is closed normally.
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		cmd := exec.CommandContext(shutdown, executable(mysqlDir, "bin", "mysqladmin"), "--no-defaults", "--protocol=tcp", "-h", "127.0.0.1", "-P", port(dbAddr), "-uroot", "shutdown")
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+password)
		// The other services stop first, before shutting down their database.
		for i := len(started) - 1; i > 0; i-- {
			started[i].close()
		}
		_ = cmd.Run()
	}()
	temporalPath := filepath.Join(bundle, "temporal")
	sqlTool := executable(temporalPath, "temporal-sql-tool")
	schemaReady := filepath.Join(state, "temporal-schema-1.32.2")
	if _, err = os.Stat(schemaReady); errors.Is(err, os.ErrNotExist) {
		for i, name := range []string{"temporal", "temporal_visibility"} {
			schema := []string{"mysql/v8/temporal", "mysql/v8/visibility"}[i]
			env := append(os.Environ(), "SQL_PASSWORD="+password)
			flags := []string{"--endpoint", "127.0.0.1", "--port", port(dbAddr), "--user", "classroom", "--database", name}
			if err = command(ctx, logDir, name+"-schema", sqlTool, append(flags, "setup-schema", "-v", "0.0"), env); err != nil {
				return err
			}
			if err = command(ctx, logDir, name+"-migration", sqlTool, append(flags, "update-schema", "--schema-name", schema), env); err != nil {
				return err
			}
		}
		if err = os.WriteFile(schemaReady, nil, 0600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	template, err := os.ReadFile(filepath.Join(bundle, "temporal.yaml"))
	if err != nil {
		return err
	}
	replacements := []string{"@DB@", dbAddr, "@PASSWORD@", password, "@FRONTEND@", port(temporalAddr)}
	for i, label := range []string{"FRONTEND_MEMBER", "HISTORY", "HISTORY_MEMBER", "MATCHING", "MATCHING_MEMBER", "WORKER", "WORKER_MEMBER"} {
		replacements = append(replacements, "@"+label+"@", port(ports[5+i]))
	}
	temporalConfig := strings.NewReplacer(replacements...).Replace(string(template))
	path := filepath.Join(state, "temporal.yaml")
	if err = os.WriteFile(path, []byte(temporalConfig), 0600); err != nil {
		return err
	}
	temporal, err := start("temporal", executable(temporalPath, "temporal-server"), []string{"--config-file", path, "start"}, nil, nil, nil)
	if err != nil {
		return err
	}
	if err = waitTCP(ctx, temporal, temporalAddr, 60*time.Second); err != nil {
		return err
	}

	env := append(os.Environ(), "WORKBENCH_HOST_TOKEN="+hostToken, "WORKBENCH_MYSQL_DSN="+dsn.FormatDSN(),
		"TEMPORAL_ADDRESS="+temporalAddr, "TEMPORAL_NAMESPACE=classroom", "WORKBENCH_LISTEN="+workerAddr,
		"WORKBENCH_WORKER_URL=http://"+workerAddr, "HARNESS_BRIDGE_URL=http://"+bridgeAddr, "WORKBENCH_BRIDGE_PORT="+port(bridgeAddr),
		"WORKBENCH_REPLY_PRACTICE=1", "WORKBENCH_RESOURCE_ACL_EDIT=1", "WORKBENCH_PUBLIC_HOST=127.0.0.1",
		"WORKBENCH_DEPLOYMENT_MARKER="+filepath.Join(state, "deployment-marker"),
		"WORKBENCH_ACTIVATION_REQUIRED=1", "WORKBENCH_ACTIVATION_ISSUER="+activation.Issuer,
		"WORKBENCH_ACTIVATION_PUBLIC_KEY_FILE="+pub, "WORKBENCH_ACTIVATION_DIR="+filepath.Join(state, "activation"),
		"WORKBENCH_ACTIVATION_SIGNING_KEY_FILE=", "WORKBENCH_TOOL_SERVERS=[]", "WORKBENCH_CUSTOMER_CLIENTS=",
		"WORKBENCH_VAULT_ROOT="+filepath.Join(state, "vaults"), "WORKBENCH_PERSONAL_WORKSPACE="+filepath.Join(state, "personal"),
		"DSH_BUNDLED_SKILL_DIR="+filepath.Join(bundle, "skills"))
	runtimeDir := filepath.Join(bundle, "runtime")
	dshHome := filepath.Join(state, "dsh")
	if err = os.MkdirAll(dshHome, 0700); err != nil {
		return err
	}
	if err = linkModules(dshHome, filepath.Join(runtimeDir, "node_modules")); err != nil {
		return err
	}
	env = append(env, "DSH_HOME="+dshHome)
	workerExe := executable(bundle, "workbench-worker")
	if err = command(ctx, logDir, "namespace", workerExe, []string{"init-temporal"}, env); err != nil {
		return err
	}
	worker, err := start("worker", workerExe, nil, env, nil, nil)
	if err != nil {
		return err
	}
	if err = waitTCP(ctx, worker, workerAddr, 40*time.Second); err != nil {
		return err
	}

	fmt.Println("正在打开课堂工作台")
	node := executable(bundle, "node", "bin", "node")
	if runtime.GOOS == "windows" {
		node = executable(bundle, "node", "node")
	}
	launch := make(chan string, 1)
	host, err := start("host", node, []string{filepath.Join(runtimeDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"),
		"web", "--patch", filepath.Join(runtimeDir, "harness", "cordis.patch.yml"), "--no-open", "--host", "127.0.0.1", "--port", port(webAddr)}, env, nil, launch)
	if err != nil {
		return err
	}
	select {
	case link := <-launch:
		fmt.Println(link)
		if os.Getenv("WORKBENCH_CLASSROOM_NO_OPEN") != "1" {
			if err = openBrowser(link); err != nil {
				fmt.Println("请复制上面的本人访问链接到浏览器")
			}
		}
	case <-host.done:
		return errors.New("Harness 启动失败，请查看 host.log")
	case <-ctx.Done():
		return nil
	case <-time.After(60 * time.Second):
		return errors.New("等待 Harness 超时，请查看 host.log")
	}
	fmt.Println("保持此窗口运行，按 Ctrl+C 结束。")
	select {
	case <-ctx.Done():
		return nil
	case <-host.done:
		return errors.New("Harness 已退出，请查看 host.log")
	case <-worker.done:
		return errors.New("工作台服务已退出，请查看 worker.log")
	case <-temporal.done:
		return errors.New("Temporal 已退出，请查看 temporal.log")
	case <-db.done:
		return errors.New("MySQL 已退出，请查看 mysql.log")
	}
}

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (c *child) close() {
	select {
	case <-c.done:
		return
	default:
	}
	_ = c.cmd.Process.Kill()
	<-c.done
}

func startChild(ctx context.Context, dir, name, exe string, args, env []string, stdin io.Reader, launch chan<- string) (*child, error) {
	log, err := os.OpenFile(filepath.Join(dir, name+".log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stderr = log
	cmd.Stdout = log
	var stdout io.ReadCloser
	if launch != nil {
		cmd.Stdout = nil
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			log.Close()
			return nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		log.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		log.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	c := &child{cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(c.done)
		defer log.Close()
		if stdout != nil {
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 8192), 256<<10)
			for scanner.Scan() {
				line := scanner.Text()
				if i := strings.Index(line, "http://127.0.0.1:"); i >= 0 && strings.Contains(line[:i], "dsh web:") {
					link := strings.Fields(line[i:])[0]
					select {
					case launch <- link:
					default:
					}
					fmt.Fprintln(log, "Harness browser URL ready")
				} else {
					fmt.Fprintln(log, line)
				}
			}
		}
		_ = cmd.Wait()
	}()
	return c, nil
}

func command(ctx context.Context, dir, name, exe string, args, env []string) error {
	log, err := os.OpenFile(filepath.Join(dir, name+".log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("%s 失败，请查看 %s.log：%w", name, name, err)
	}
	return nil
}

func waitTCP(ctx context.Context, c *child, addr string, max time.Duration) error {
	timer := time.NewTimer(max)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return fmt.Errorf("服务 %s 提前退出", filepath.Base(c.cmd.Path))
		case <-timer.C:
			return fmt.Errorf("服务 %s 启动超时", filepath.Base(c.cmd.Path))
		case <-ticker.C:
		}
	}
}

// Temporal records cluster member addresses in MySQL. Keep those ports stable
// across restarts so persisted workflows resume against the same cluster.
func instancePorts(state string) ([]string, error) {
	path := filepath.Join(state, "ports.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		ports, err := localPorts(12)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(ports)
		if err != nil {
			return nil, err
		}
		return ports, os.WriteFile(path, data, 0600)
	}
	if err != nil {
		return nil, err
	}
	var ports []string
	if err = json.Unmarshal(raw, &ports); err != nil || len(ports) != 12 {
		return nil, errors.New("本机端口配置损坏")
	}
	var sockets []net.Listener
	defer func() {
		for _, socket := range sockets {
			_ = socket.Close()
		}
	}()
	for _, addr := range ports {
		host, service, err := net.SplitHostPort(addr)
		if err != nil || host != "127.0.0.1" || service == "0" {
			return nil, errors.New("本机端口配置损坏")
		}
		socket, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("本机端口 %s 被占用，请关闭占用它的程序", service)
		}
		sockets = append(sockets, socket)
	}
	return ports, nil
}

func localPorts(count int) ([]string, error) {
	var sockets []net.Listener
	defer func() {
		for _, s := range sockets {
			s.Close()
		}
	}()
	addresses := make([]string, 0, count)
	for attempts := 0; len(addresses) < count && attempts < 256; attempts++ {
		var random [2]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		s, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", 15000+int(random[0])*64+int(random[1]&63)))
		if err != nil {
			continue
		}
		sockets = append(sockets, s)
		addresses = append(addresses, s.Addr().String())
	}
	if len(addresses) != count {
		return nil, errors.New("未能分配足够的本机端口")
	}
	return addresses, nil
}

func port(addr string) string { _, p, _ := net.SplitHostPort(addr); return p }

func instancePassword(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if err == nil {
		if decoded, e := hex.DecodeString(string(raw)); e != nil || len(decoded) != 32 {
			return "", errors.New("本机数据库凭证文件损坏")
		}
		return string(raw), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	value := make([]byte, 32)
	if _, err = rand.Read(value); err != nil {
		return "", err
	}
	password := hex.EncodeToString(value)
	return password, os.WriteFile(path, []byte(password), 0600)
}

func executable(parts ...string) string {
	name := filepath.Join(parts...)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func openBrowser(link string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", link).Start()
	}
	return exec.Command("open", link).Start()
}
