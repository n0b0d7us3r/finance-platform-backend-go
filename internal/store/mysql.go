package store

import (
	"database/sql"
	"net"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// OpenMySQL opens a pool. The driver parses timestamps in timezone.
func OpenMySQL(host, port, user, password, database, timezone string) (*sql.DB, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	dsn := mysqldriver.Config{
		User:                 user,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 net.JoinHostPort(host, port),
		DBName:               database,
		ParseTime:            true,
		Collation:            "utf8mb4_unicode_ci",
		AllowNativePasswords: true,
		Params:               map[string]string{"charset": "utf8mb4"},
		Loc:                  loc,
	}
	return sql.Open("mysql", dsn.FormatDSN())
}
