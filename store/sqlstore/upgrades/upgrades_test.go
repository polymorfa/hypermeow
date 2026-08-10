package upgrades

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.mau.fi/util/dbutil"
)

var upgradeVersionPattern = regexp.MustCompile(`^-- (?:v(\d+) -> )?v(\d+)`)

func TestUpgradeSourcesHaveUniqueStartingVersions(t *testing.T) {
	entries, err := fs.ReadDir(upgrades, ".")
	if err != nil {
		t.Fatal(err)
	}
	versions := make(map[int]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		file, err := upgrades.Open(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		if !scanner.Scan() {
			_ = file.Close()
			t.Fatalf("%s has no upgrade header", entry.Name())
		}
		_ = file.Close()
		matches := upgradeVersionPattern.FindStringSubmatch(scanner.Text())
		if matches == nil {
			t.Fatalf("%s has an invalid upgrade header", entry.Name())
		}
		to, err := strconv.Atoi(matches[2])
		if err != nil {
			t.Fatal(err)
		}
		from := to - 1
		if matches[1] != "" {
			from, err = strconv.Atoi(matches[1])
			if err != nil {
				t.Fatal(err)
			}
		}
		if previous, exists := versions[from]; exists {
			t.Fatalf("%s and %s both register an upgrade starting at v%d", previous, entry.Name(), from)
		}
		versions[from] = entry.Name()
	}
}

type upgradeTestState struct {
	executed      []string
	version       int64
	compatVersion int64
}

type upgradeTestConnector struct{ state *upgradeTestState }

func (c *upgradeTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &upgradeTestConn{state: c.state}, nil
}

func (*upgradeTestConnector) Driver() driver.Driver { return upgradeTestDriver{} }

type upgradeTestDriver struct{}

func (upgradeTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type upgradeTestConn struct{ state *upgradeTestState }

func (*upgradeTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}

func (*upgradeTestConn) Close() error { return nil }

func (c *upgradeTestConn) Begin() (driver.Tx, error) { return &upgradeTestTx{}, nil }

func (c *upgradeTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &upgradeTestTx{}, nil
}

func (c *upgradeTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "information_schema.columns"):
		return &upgradeTestRows{columns: []string{"exists"}, values: []driver.Value{true}}, nil
	case strings.HasPrefix(query, "SELECT version, compat FROM whatsmeow_version"):
		return &upgradeTestRows{columns: []string{"version", "compat"}, values: []driver.Value{c.state.version, c.state.compatVersion}}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
}

func (c *upgradeTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.state.executed = append(c.state.executed, query)
	if strings.HasPrefix(query, "INSERT INTO whatsmeow_version") {
		if len(args) != 2 {
			return nil, fmt.Errorf("unexpected version argument count: %d", len(args))
		}
		c.state.version = args[0].Value.(int64)
		c.state.compatVersion = args[1].Value.(int64)
	}
	return driver.RowsAffected(1), nil
}

type upgradeTestTx struct{}

func (*upgradeTestTx) Commit() error   { return nil }
func (*upgradeTestTx) Rollback() error { return nil }

type upgradeTestRows struct {
	columns []string
	values  []driver.Value
	read    bool
}

func (r *upgradeTestRows) Columns() []string { return r.columns }
func (*upgradeTestRows) Close() error        { return nil }
func (r *upgradeTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(values, r.values)
	return nil
}

func TestUpgradeFromCurrentDevSchemaAddsUsername(t *testing.T) {
	state := &upgradeTestState{version: 17, compatVersion: 8}
	rawDB := sql.OpenDB(&upgradeTestConnector{state: state})
	t.Cleanup(func() { _ = rawDB.Close() })
	db, err := dbutil.NewWithDB(rawDB, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	db.VersionTable = "whatsmeow_version"
	db.UpgradeTable = Table
	if err = db.Upgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state.version != 18 || state.compatVersion != 8 {
		t.Fatalf("schema version = %d/%d, want 18/8", state.version, state.compatVersion)
	}
	want := "ALTER TABLE whatsmeow_contacts ADD COLUMN username TEXT;"
	for _, query := range state.executed {
		if strings.TrimSpace(query) == want {
			return
		}
	}
	t.Fatalf("username migration was not executed: %q", state.executed)
}
