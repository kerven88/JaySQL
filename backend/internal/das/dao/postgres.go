package dao

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresDB struct {
	User     string
	Password string
	Host     string
	Port     int
	Database string
	SSLMode  string
	Ctx      context.Context
}

func (d *PostgresDB) connString() string {
	sslMode := d.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	database := d.Database
	if database == "" {
		database = "postgres"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s", d.User, d.Password, d.Host, d.Port, database, sslMode)
}

func (d *PostgresDB) Query(query string) (*[]string, *[]map[string]interface{}, error) {
	conn, err := pgx.Connect(d.Ctx, d.connString())
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close(d.Ctx)

	rows, err := conn.Query(d.Ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	columns := make([]string, 0, len(fields))
	for _, field := range fields {
		columns = append(columns, string(field.Name))
	}

	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, nil, err
		}
		row := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			if values[i] == nil {
				row[column] = nil
				continue
			}
			row[column] = formatPostgresCellValue(values[i])
		}
		result = append(result, row)
	}
	if rows.Err() != nil {
		return nil, nil, rows.Err()
	}
	return &columns, &result, nil
}

func (d *PostgresDB) Execute(query string) error {
	conn, err := pgx.Connect(d.Ctx, d.connString())
	if err != nil {
		return err
	}
	defer conn.Close(d.Ctx)
	_, err = conn.Exec(d.Ctx, query)
	return err
}

func (d *PostgresDB) QueryWithSearchPath(schemaName string, query string) (*[]string, *[]map[string]interface{}, error) {
	conn, err := pgx.Connect(d.Ctx, d.connString())
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close(d.Ctx)

	if strings.TrimSpace(schemaName) != "" {
		escaped := `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `"`
		_, err := conn.Exec(d.Ctx, fmt.Sprintf("set search_path to %s", escaped))
		if err != nil {
			return nil, nil, err
		}
	}

	rows, err := conn.Query(d.Ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	columns := make([]string, 0, len(fields))
	for _, field := range fields {
		columns = append(columns, string(field.Name))
	}

	result := make([]map[string]interface{}, 0)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, nil, err
		}
		row := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			if values[i] == nil {
				row[column] = nil
				continue
			}
			row[column] = formatPostgresCellValue(values[i])
		}
		result = append(result, row)
	}
	if rows.Err() != nil {
		return nil, nil, rows.Err()
	}
	return &columns, &result, nil
}

func formatPostgresCellValue(value any) any {
	switch v := value.(type) {
	case pgtype.Numeric:
		return formatPgNumeric(v)
	case *pgtype.Numeric:
		if v == nil {
			return nil
		}
		return formatPgNumeric(*v)
	case pgtype.Interval:
		return formatPgInterval(v)
	case *pgtype.Interval:
		if v == nil {
			return nil
		}
		return formatPgInterval(*v)
	case time.Duration:
		return formatGoDuration(v)
	case *time.Duration:
		if v == nil {
			return nil
		}
		return formatGoDuration(*v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func formatPgNumeric(n pgtype.Numeric) any {
	if !n.Valid {
		return nil
	}
	dv, err := n.Value()
	if err == nil && dv != nil {
		if s, ok := dv.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", dv)
	}
	return fmt.Sprintf("%v", n)
}

func formatPgInterval(interval pgtype.Interval) string {
	if !interval.Valid {
		return ""
	}

	months := interval.Months
	days := interval.Days
	micros := interval.Microseconds

	sign := ""
	if months < 0 || days < 0 || micros < 0 {
		sign = "-"
	}

	absMonths := months
	if absMonths < 0 {
		absMonths = -absMonths
	}
	absDays := days
	if absDays < 0 {
		absDays = -absDays
	}
	absMicros := micros
	if absMicros < 0 {
		absMicros = -absMicros
	}

	parts := make([]string, 0, 3)
	if absMonths != 0 {
		parts = append(parts, fmt.Sprintf("%dmon", absMonths))
	}
	if absDays != 0 {
		parts = append(parts, fmt.Sprintf("%dd", absDays))
	}

	d := time.Duration(absMicros) * time.Microsecond
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second
	d -= seconds * time.Second
	remainMicros := d / time.Microsecond

	timePart := fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	if remainMicros != 0 {
		timePart = fmt.Sprintf("%s.%06d", timePart, remainMicros)
	}

	if len(parts) > 0 {
		return sign + strings.Join(append(parts, timePart), " ")
	}
	return sign + timePart
}

func formatGoDuration(d time.Duration) string {
	if d == 0 {
		return "00:00:00"
	}

	sign := ""
	if d < 0 {
		sign = "-"
		d = -d
	}

	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second
	d -= seconds * time.Second

	timePart := fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	remainMicros := d / time.Microsecond
	if remainMicros != 0 {
		timePart = fmt.Sprintf("%s.%06d", timePart, remainMicros)
	}
	return sign + timePart
}
