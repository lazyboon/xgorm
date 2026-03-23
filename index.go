package xgorm

import (
	"reflect"

	"gorm.io/gorm"
	gormschema "gorm.io/gorm/schema"
)

// getTableName resolves the database table name from a model or an explicit name.
// It prioritizes GORM's NamingStrategy and TableName() interface.
func getTableName(db *gorm.DB, model any, explicitTable string) string {
	if explicitTable != "" {
		return explicitTable
	}
	if model == nil {
		return ""
	}

	// 1. Try to use GORM's statement parser (handles NamingStrategy, Pluralization, etc.)
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err == nil && stmt.Schema != nil {
		return stmt.Schema.Table
	}

	// 2. Fallback: Check if the model implements the TableName interface
	if tn, ok := model.(interface{ TableName() string }); ok {
		return tn.TableName()
	}

	// 3. Last Resort: Reflect the type name and use default GORM naming strategy
	t := reflect.TypeOf(model)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return gormschema.NamingStrategy{}.TableName(t.Name())
}
