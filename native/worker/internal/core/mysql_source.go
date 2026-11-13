package core

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// MySQLSourceError exposes an actionable reason without a DSN or raw SQL error.
type MySQLSourceError struct {
	Code string
	Text string
}

func (e *MySQLSourceError) Error() string { return e.Text }

func mysqlSourceError(err error) *MySQLSourceError {
	var e *mysql.MySQLError
	if errors.As(err, &e) {
		switch e.Number {
		case 1044, 1142, 1143, 1227:
			return &MySQLSourceError{"mysql_permission", "数据库账号缺少读取权限。请让数据库管理员为该库、表或字段授予 SELECT 权限。"}
		case 1045, 1698:
			return &MySQLSourceError{"mysql_auth", "数据库拒绝了账号凭证。请联系部署者和数据库管理员核对账号及密码。"}
		case 1046, 1049:
			return &MySQLSourceError{"mysql_database", "找不到配置中的数据库。请让部署者和数据库管理员核对库名。"}
		case 1146, 1356:
			return &MySQLSourceError{"mysql_table", "无法读取指定表或视图。请核对表名，或联系数据库管理员检查视图和权限。"}
		case 1054:
			return &MySQLSourceError{"mysql_field", "表中找不到指定字段。请核对唯一编号、资料板块及版本字段。"}
		}
	}
	return &MySQLSourceError{"mysql_unavailable", "无法连接或读取外接 MySQL。请让部署者检查网络、地址和端口，或请数据库管理员检查服务。"}
}

func openMySQLSource(ctx context.Context, connection Connection) (*Store, error) {
	if connection.Kind != "mysql" || !ValidID(connection.SecretEnv) || !strings.HasPrefix(connection.SecretEnv, "WORKBENCH_SOURCE_") {
		return nil, &MySQLSourceError{"mysql_reference", "凭证引用格式有误。请向部署者获取以 WORKBENCH_SOURCE_ 开头的变量名。"}
	}
	dsn := os.Getenv(connection.SecretEnv)
	if strings.TrimSpace(dsn) == "" {
		return nil, &MySQLSourceError{"mysql_missing", "服务器没有配置该凭证引用。请联系部署者配置，或选择已配置的连接。"}
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || cfg.DBName == "" {
		return nil, &MySQLSourceError{"mysql_dsn", "服务器上的 MySQL 凭证格式有误或未指定数据库。请联系部署者修正。"}
	}
	source, err := Open(ctx, dsn)
	if err != nil {
		return nil, mysqlSourceError(err)
	}
	return source, nil
}

func TestMySQLConnection(ctx context.Context, p Principal, connection Connection) error {
	if !p.IsOwner() {
		return ErrDenied
	}
	source, err := openMySQLSource(ctx, connection)
	if err != nil {
		return err
	}
	return source.DB.Close()
}

func mysqlColumns(r Resource) ([]string, error) {
	if !ValidID(r.Relation) || !ValidID(r.KeyField) {
		return nil, &MySQLSourceError{"mysql_mapping", "请填写表或视图名称和唯一编号字段。"}
	}
	fields := map[string]bool{r.KeyField: true}
	if r.WatermarkField != "" {
		fields[r.WatermarkField] = true
	}
	for _, section := range r.Sections {
		for _, field := range section.Fields {
			fields[field] = true
		}
	}
	columns := make([]string, 0, len(fields))
	for field := range fields {
		if !ValidID(field) {
			return nil, &MySQLSourceError{"mysql_mapping", "字段名仅支持字母、数字、下划线或连字符。"}
		}
		columns = append(columns, "`"+field+"`")
	}
	sort.Strings(columns)
	return columns, nil
}

func checkMySQLColumns(ctx context.Context, source interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, resource Resource) error {
	columns, err := mysqlColumns(resource)
	if err != nil {
		return err
	}
	rows, err := source.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM `"+resource.Relation+"` LIMIT 0")
	if err != nil {
		return mysqlSourceError(err)
	}
	return rows.Close()
}

func (s *Store) checkResourceConnection(ctx context.Context, resource Resource) error {
	if resource.ConnectionID == "" {
		return nil
	}
	connection, err := s.sourceConnection(ctx, resource)
	if IsMissing(err) {
		return &MySQLSourceError{"source_missing", "找不到所选连接。请先保存连接，再选择它。"}
	}
	if err != nil || connection.Kind != "mysql" {
		return err
	}
	source, err := openMySQLSource(ctx, connection)
	if err != nil {
		return err
	}
	defer source.DB.Close()
	return checkMySQLColumns(ctx, source.DB, resource)
}

func (s *Store) RebindMySQLResource(ctx context.Context, p Principal, id, connectionID string, revision int64) (Resource, error) {
	if !p.IsOwner() {
		return Resource{}, ErrDenied
	}
	r, err := s.Resource(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Revision != revision {
		return r, ErrConflict
	}
	current, err := s.sourceConnection(ctx, r)
	if err != nil || current.Kind != "mysql" || !ValidID(connectionID) {
		return r, ErrInvalid
	}
	r.ConnectionID = connectionID
	next, err := s.sourceConnection(ctx, r)
	if err != nil || next.Kind != "mysql" {
		return r, ErrInvalid
	}
	if err := s.RegisterResource(ctx, p, r); err != nil {
		return r, err
	}
	return s.Resource(ctx, id)
}

// An interrupted MySQL read cannot resume its old repeatable-read transaction.
// Reread all rows for this run from one transaction, discarding only its staging.
func (s *Store) resetMySQLStaging(ctx context.Context, p Principal, id, version string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := guardWrite(ctx, tx); err != nil {
		return err
	}
	if _, err := s.lockSnapshotWrite(ctx, tx, p, id); err != nil {
		return err
	}
	var state string
	err = tx.QueryRowContext(ctx, "SELECT state FROM wb_snapshots WHERE resource_id=? AND version=? FOR UPDATE", id, version).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "staging" {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM wb_records WHERE resource_id=? AND version=?", id, version); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM wb_snapshots WHERE resource_id=? AND version=? AND state='staging'", id, version); err != nil {
		return err
	}
	return tx.Commit()
}
