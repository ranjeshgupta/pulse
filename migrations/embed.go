// Package migrations embeds the SQL schema so the binary can migrate itself (`pulse migrate`).
package migrations

import "embed"

//go:embed postgres/*.sql
var Postgres embed.FS

//go:embed clickhouse/*.sql
var ClickHouse embed.FS
