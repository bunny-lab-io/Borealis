package main

import (
	"context"
	"fmt"
)

var assemblyTableColumnDefinitions = []string{
	"assembly_guid TEXT PRIMARY KEY",
	"display_name TEXT NOT NULL",
	"summary TEXT",
	"assembly_type TEXT NOT NULL",
	"assembly_subtype TEXT",
	"payload_json TEXT NOT NULL",
	"source_repo TEXT",
	"source_path TEXT",
	"source_version TEXT",
	"content_hash TEXT",
	"payload_size_bytes BIGINT NOT NULL DEFAULT 0",
	"created_at TEXT NOT NULL",
	"updated_at TEXT NOT NULL",
}

var officialCatalogStateColumnDefinitions = []string{
	"assembly_guid TEXT PRIMARY KEY",
	"bundled_hash TEXT",
	"remote_hash TEXT",
	"catalog_hash TEXT",
	"applied_hash TEXT",
	"last_applied_source TEXT",
	"repo_url TEXT",
	"source_url TEXT",
	"source_repo TEXT",
	"source_path TEXT",
	"source_version TEXT",
	"last_catalog_sync_at TEXT",
	"updated_at TEXT NOT NULL",
}

func (s *postgresOperatorStore) ensureAssemblyTables(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, statement := range goAssemblySchemaStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func goAssemblySchemaStatements() []string {
	statements := []string{`CREATE SCHEMA IF NOT EXISTS assemblies`}
	statements = append(statements, goAssemblyTableStatements(officialCatalogStateQualifiedTable, officialCatalogStateColumnDefinitions, "")...)
	for _, domain := range assemblyDomains() {
		tableName := assemblyQualifiedTable(domain)
		indexName := fmt.Sprintf("idx_%s_assembly_type", assemblyTableSuffix(domain))
		statements = append(statements, goAssemblyTableStatements(tableName, assemblyTableColumnDefinitions, indexName)...)
	}
	return statements
}

func goAssemblyTableStatements(tableName string, columnDefinitions []string, typeIndexName string) []string {
	statements := []string{
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", tableName, joinSQLFragments(columnDefinitions, ", ")),
	}
	for _, definition := range columnDefinitions {
		columnName := firstSQLIdentifier(definition)
		if columnName == "" {
			continue
		}
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s", tableName, definition))
	}
	if typeIndexName != "" {
		statements = append(statements, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(assembly_type)", typeIndexName, tableName))
	}
	return statements
}

func assemblyTableSuffix(domain string) string {
	switch domain {
	case assemblyDomainOfficial:
		return "official_assemblies"
	case assemblyDomainCommunity:
		return "community_assemblies"
	default:
		return "user_created_assemblies"
	}
}

func joinSQLFragments(values []string, separator string) string {
	out := ""
	for _, value := range values {
		if out != "" {
			out += separator
		}
		out += value
	}
	return out
}

func firstSQLIdentifier(definition string) string {
	for index, char := range definition {
		if char == ' ' || char == '\t' || char == '\n' || char == '\r' {
			return definition[:index]
		}
	}
	return definition
}
