package xgorm

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------------------------------------------------

const (
	// postgresMaxIdentifierLength is the maximum length for any PostgreSQL identifier.
	// Default NAMEDATALEN in Postgres is 64, so the max identifier length is 63.
	postgresMaxIdentifierLength = 63

	// defaultPostgresSchema is the fallback schema used for catalog queries when no schema is provided.
	defaultPostgresSchema = "public"
)

var (
	// postgresReSimpleIdent defines the pattern for standard identifiers that do not require double quotes.
	postgresReSimpleIdent = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

	// postgresReSafeOpClass validates operator class names to prevent SQL injection or syntax errors.
	postgresReSafeOpClass = regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)
)

// ---------------------------------------------------------------------------------------------------------------------

// PostgresIndexMethod represents the Access Method (AM) used by the index.
type PostgresIndexMethod string

const (
	// PostgresIndexBTree is the default B-Tree index. Suitable for most data types and range queries.
	PostgresIndexBTree PostgresIndexMethod = "BTREE"

	// PostgresIndexHash is used for simple equality comparisons.
	PostgresIndexHash PostgresIndexMethod = "HASH"

	// PostgresIndexGin (Generalized Inverted Index) is best for composite data like JSONB, arrays, or text search.
	PostgresIndexGin PostgresIndexMethod = "GIN"

	// PostgresIndexGist (Generalized Search Tree) is useful for indexing geometric data or full-text search.
	PostgresIndexGist PostgresIndexMethod = "GIST"

	// PostgresIndexSpGist (Space-partitioned GiST) is optimized for non-balanced tree structures.
	PostgresIndexSpGist PostgresIndexMethod = "SPGIST"

	// PostgresIndexBrin (Block Range Index) is ideal for very large tables naturally ordered by a column.
	PostgresIndexBrin PostgresIndexMethod = "BRIN"
)

// Validate checks if the index method is natively supported by PostgreSQL.
func (m PostgresIndexMethod) Validate() error {
	switch m {
	case PostgresIndexBTree, PostgresIndexHash, PostgresIndexGin, PostgresIndexGist, PostgresIndexSpGist, PostgresIndexBrin:
		return nil
	default:
		return fmt.Errorf("unsupported postgres index method: %s", m)
	}
}

// ---------------------------------------------------------------------------------------------------------------------

// PostgresIndexOrderType specifies the sort order for indexed columns (B-Tree only).
type PostgresIndexOrderType string

const (
	// PostgresIndexOrderAsc sorts the index in ascending order.
	PostgresIndexOrderAsc PostgresIndexOrderType = "ASC"

	// PostgresIndexOrderDesc sorts the index in descending order.
	PostgresIndexOrderDesc PostgresIndexOrderType = "DESC"
)

// PostgresIndexNullsOrder specifies where NULL values are placed in the sort order (B-Tree only).
type PostgresIndexNullsOrder string

const (
	// PostgresIndexNullsFirst places nulls at the beginning of the sort order.
	PostgresIndexNullsFirst PostgresIndexNullsOrder = "NULLS FIRST"

	// PostgresIndexNullsLast places nulls at the end of the sort order.
	PostgresIndexNullsLast PostgresIndexNullsOrder = "NULLS LAST"
)

// ---------------------------------------------------------------------------------------------------------------------

// PostgresIndexOptions represents physical storage parameters configured via the WITH clause.
type PostgresIndexOptions struct {
	// fillFactor (10-100) sets the packing density of index pages.
	fillFactor int

	// fastUpdate is a GIN-specific option that speeds up insertions via a temporary list.
	fastUpdate *bool

	// ginPendingListLimit sets the maximum size (KB) of the GIN pending list.
	ginPendingListLimit int

	// pagesPerRange defines the number of table blocks per BRIN index range.
	pagesPerRange int

	// autoSummarize enables automatic BRIN summarization.
	autoSummarize *bool
}

// NewPostgresIndexOptions initializes an empty set of storage parameters.
func NewPostgresIndexOptions() *PostgresIndexOptions {
	return &PostgresIndexOptions{}
}

// FillFactor sets the fillfactor (10-100) for the index.
func (o *PostgresIndexOptions) FillFactor(f int) *PostgresIndexOptions {
	o.fillFactor = f
	return o
}

// FastUpdate enables or disables the GIN fast update feature.
func (o *PostgresIndexOptions) FastUpdate(enabled bool) *PostgresIndexOptions {
	o.fastUpdate = &enabled
	return o
}

// GinPendingListLimit sets the maximum size (KB) for the GIN pending list.
func (o *PostgresIndexOptions) GinPendingListLimit(limit int) *PostgresIndexOptions {
	o.ginPendingListLimit = limit
	return o
}

// PagesPerRange sets the number of table blocks per range for BRIN indexes.
func (o *PostgresIndexOptions) PagesPerRange(p int) *PostgresIndexOptions {
	o.pagesPerRange = p
	return o
}

// AutoSummarize enables auto-summarization for BRIN indexes.
func (o *PostgresIndexOptions) AutoSummarize(enabled bool) *PostgresIndexOptions {
	o.autoSummarize = &enabled
	return o
}

// validate checks parameter compatibility with the index method and internal dependencies.
func (o *PostgresIndexOptions) validate(method PostgresIndexMethod) error {
	if o == nil {
		return nil
	}
	if o.fillFactor != 0 && (o.fillFactor < 10 || o.fillFactor > 100) {
		return fmt.Errorf("fillfactor must be 10-100")
	}
	if method == PostgresIndexGin {
		if o.fastUpdate != nil && !*o.fastUpdate && o.ginPendingListLimit > 0 {
			return fmt.Errorf("ginPendingListLimit (%d) cannot be set when fastUpdate is disabled", o.ginPendingListLimit)
		}
	} else if o.fastUpdate != nil || o.ginPendingListLimit > 0 {
		return fmt.Errorf("GIN options are incompatible with %s", method)
	}
	if method != PostgresIndexBrin && (o.pagesPerRange > 0 || o.autoSummarize != nil) {
		return fmt.Errorf("BRIN options are incompatible with %s", method)
	}
	return nil
}

// ---------------------------------------------------------------------------------------------------------------------

// PostgresIndexColumn defines a specific column or expression used in an index.
type PostgresIndexColumn struct {
	// column is the column name or a PostgreSQL expression (e.g., "lower(email)").
	column string

	// opClass defines the operator class to use (e.g., "jsonb_path_ops").
	opClass string

	// collation defines the collation to use for string comparison.
	collation string

	// order specifies the sorting direction (ASC/DESC). B-Tree only.
	order PostgresIndexOrderType

	// nulls specifies where NULL values appear. B-Tree only.
	nulls PostgresIndexNullsOrder
}

// NewPostgresIndexColumn creates a new column definition for an index.
func NewPostgresIndexColumn(column string) *PostgresIndexColumn {
	return &PostgresIndexColumn{column: strings.TrimSpace(column)}
}

// OpClass sets the operator class for this index column.
func (c *PostgresIndexColumn) OpClass(op string) *PostgresIndexColumn {
	c.opClass = strings.TrimSpace(op)
	return c
}

// Collation sets the collation for this index column.
func (c *PostgresIndexColumn) Collation(col string) *PostgresIndexColumn {
	c.collation = strings.TrimSpace(col)
	return c
}

// Order sets the sort order (ASC/DESC) for this column.
func (c *PostgresIndexColumn) Order(o PostgresIndexOrderType) *PostgresIndexColumn {
	c.order = o
	return c
}

// Nulls sets the nulls ordering (FIRST/LAST) for this column.
func (c *PostgresIndexColumn) Nulls(n PostgresIndexNullsOrder) *PostgresIndexColumn {
	c.nulls = n
	return c
}

// ---------------------------------------------------------------------------------------------------------------------

// PostgresIndex is the primary builder for managing PostgreSQL indexes.
type PostgresIndex struct {
	// table is the explicit name of the target table.
	table string

	// model is a GORM model instance used to derive the table name.
	model any

	// schema is the PostgreSQL namespace (e.g., "public").
	schema string

	// name is the explicit index name. Auto-generated if empty.
	name string

	// method defines the index type (BTREE, GIN, etc.).
	method PostgresIndexMethod

	// columns lists the columns or expressions that form the index key.
	columns []*PostgresIndexColumn

	// unique specifies whether to enforce unique values.
	unique bool

	// concurrently allows building the index without locking the table.
	concurrently bool

	// ifNotExists adds "IF NOT EXISTS" to the CREATE statement.
	ifNotExists bool

	// where defines a partial index predicate (e.g., "status = 'active'").
	where string

	// tablespace specifies the physical storage location for the index.
	tablespace string

	// options contains physical storage parameters (WITH clause).
	options *PostgresIndexOptions

	// comment adds a description via "COMMENT ON INDEX".
	comment string
}

// NewPostgresIndex creates a new PostgresIndex builder with default settings.
func NewPostgresIndex() *PostgresIndex {
	return &PostgresIndex{
		method:      PostgresIndexBTree,
		ifNotExists: true,
	}
}

// Table sets the explicit table name.
func (p *PostgresIndex) Table(t string) *PostgresIndex {
	p.table = strings.TrimSpace(t)
	return p
}

// Model sets the GORM model for table name derivation.
func (p *PostgresIndex) Model(m any) *PostgresIndex {
	p.model = m
	return p
}

// Schema sets the target database schema.
func (p *PostgresIndex) Schema(s string) *PostgresIndex {
	p.schema = strings.TrimSpace(s)
	return p
}

// Name sets the explicit index name.
func (p *PostgresIndex) Name(n string) *PostgresIndex {
	p.name = strings.TrimSpace(n)
	return p
}

// Method sets the index access strategy.
func (p *PostgresIndex) Method(m PostgresIndexMethod) *PostgresIndex {
	p.method = m
	return p
}

// Columns adds complex column definitions to the index.
func (p *PostgresIndex) Columns(cols ...*PostgresIndexColumn) *PostgresIndex {
	p.columns = append(p.columns, cols...)
	return p
}

// Fields adds simple column names as index keys.
func (p *PostgresIndex) Fields(fields ...string) *PostgresIndex {
	for _, f := range fields {
		p.columns = append(p.columns, NewPostgresIndexColumn(f))
	}
	return p
}

// Unique enables unique constraints for B-Tree indexes.
func (p *PostgresIndex) Unique(u bool) *PostgresIndex {
	p.unique = u
	return p
}

// Concurrently enables non-blocking index creation.
func (p *PostgresIndex) Concurrently(c bool) *PostgresIndex {
	p.concurrently = c
	return p
}

// IfNotExists toggles the "IF NOT EXISTS" safety clause.
func (p *PostgresIndex) IfNotExists(e bool) *PostgresIndex {
	p.ifNotExists = e
	return p
}

// Where sets the partial index condition.
func (p *PostgresIndex) Where(w string) *PostgresIndex {
	p.where = strings.TrimSpace(w)
	return p
}

// Tablespace sets the storage tablespace.
func (p *PostgresIndex) Tablespace(t string) *PostgresIndex {
	p.tablespace = strings.TrimSpace(t)
	return p
}

// Options sets storage parameters for the index.
func (p *PostgresIndex) Options(o *PostgresIndexOptions) *PostgresIndex {
	p.options = o
	return p
}

// Comment sets the index description.
func (p *PostgresIndex) Comment(c string) *PostgresIndex {
	p.comment = strings.TrimSpace(c)
	return p
}

// ToSQL compiles the index definition into PostgreSQL DDL statements.
func (p *PostgresIndex) ToSQL() ([]string, error) {
	table := p.ResolveTableName()
	if table == "" || len(p.columns) == 0 {
		return nil, fmt.Errorf("index generation failed: table and columns are required")
	}

	if err := p.method.Validate(); err != nil {
		return nil, err
	}
	if p.options != nil {
		if err := p.options.validate(p.method); err != nil {
			return nil, err
		}
	}

	var buf strings.Builder
	buf.WriteString("CREATE")
	if p.unique && p.method == PostgresIndexBTree {
		buf.WriteString(" UNIQUE")
	}
	buf.WriteString(" INDEX")

	if p.concurrently {
		buf.WriteString(" CONCURRENTLY")
	}
	if p.ifNotExists {
		buf.WriteString(" IF NOT EXISTS")
	}

	idxName := p.ResolveIndexName()
	fullTableName := p.quoteIdent(table)
	if p.schema != "" {
		fullTableName = p.quoteIdent(p.schema) + "." + fullTableName
	}

	buf.WriteString(fmt.Sprintf(" %s ON %s ", p.quoteIdent(idxName), fullTableName))
	buf.WriteString("USING " + string(p.method) + " ")

	var colSpecs []string
	for _, c := range p.columns {
		spec := c.column
		if p.isSimpleColumn(spec) {
			spec = p.quoteIdent(spec)
		}
		if c.collation != "" {
			spec += " COLLATE " + p.quoteIdent(c.collation)
		}
		if c.opClass != "" {
			op, err := p.safeOpClass(c.opClass)
			if err != nil {
				return nil, err
			}
			spec += " " + op
		}
		if p.method == PostgresIndexBTree {
			if c.order != "" {
				spec += " " + string(c.order)
			}
			if c.nulls != "" {
				spec += " " + string(c.nulls)
			}
		}
		colSpecs = append(colSpecs, spec)
	}

	buf.WriteString("(" + strings.Join(colSpecs, ", ") + ")")

	if p.options != nil {
		var opts []string
		o := p.options
		if o.fillFactor > 0 {
			opts = append(opts, fmt.Sprintf("fillfactor = %d", o.fillFactor))
		}
		if o.fastUpdate != nil {
			val := "off"
			if *o.fastUpdate {
				val = "on"
			}
			opts = append(opts, "fastupdate = "+val)
		}
		if o.ginPendingListLimit > 0 {
			opts = append(opts, fmt.Sprintf("gin_pending_list_limit = %d", o.ginPendingListLimit))
		}
		if o.pagesPerRange > 0 {
			opts = append(opts, fmt.Sprintf("pages_per_range = %d", o.pagesPerRange))
		}
		if o.autoSummarize != nil {
			val := "off"
			if *o.autoSummarize {
				val = "on"
			}
			opts = append(opts, "autosummarize = "+val)
		}
		if len(opts) > 0 {
			buf.WriteString(" WITH (" + strings.Join(opts, ", ") + ")")
		}
	}

	if p.tablespace != "" {
		buf.WriteString(" TABLESPACE " + p.quoteIdent(p.tablespace))
	}
	if p.where != "" {
		buf.WriteString(" WHERE " + p.where)
	}
	buf.WriteString(";")

	results := []string{buf.String()}
	if p.comment != "" {
		results = append(results, fmt.Sprintf("COMMENT ON INDEX %s IS %s;", p.quoteIdent(idxName), p.quoteLiteral(p.comment)))
	}

	return results, nil
}

// ---------------------------------------------------------------------------------------------------------------------

// quoteIdent safely escapes PostgreSQL identifiers using double quotes.
func (p *PostgresIndex) quoteIdent(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, ".")
	for i, part := range parts {
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

// quoteLiteral escapes and wraps string literals for SQL statements.
func (p *PostgresIndex) quoteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

// isSimpleColumn identifies if a column name is a simple identifier or a complex expression.
func (p *PostgresIndex) isSimpleColumn(col string) bool {
	return postgresReSimpleIdent.MatchString(col)
}

// safeOpClass ensures operator class names are syntactically safe.
func (p *PostgresIndex) safeOpClass(op string) (string, error) {
	if op == "" || postgresReSafeOpClass.MatchString(op) {
		return op, nil
	}
	return "", fmt.Errorf("unsafe operator class name: %s", op)
}

// ResolveTableName resolves the table name while preserving case sensitivity.
func (p *PostgresIndex) ResolveTableName() string {
	if p.table != "" {
		return p.table
	}
	if p.model == nil {
		return ""
	}
	if tn, ok := p.model.(interface{ TableName() string }); ok {
		return tn.TableName()
	}
	t := reflect.TypeOf(p.model)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	name := t.Name()
	var sb strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			sb.WriteRune('_')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// ResolveIndexName generates a unique name. Uses FNV-64a hash to prevent truncation collisions on long names.
func (p *PostgresIndex) ResolveIndexName() string {
	if p.name != "" {
		return p.name
	}
	prefix := "idx"
	if p.unique {
		prefix = "uk"
	}

	rawTable := strings.NewReplacer("\"", "", "'", "", ".", "_").Replace(p.ResolveTableName())
	colMapper := strings.NewReplacer("(", "_", ")", "", " ", "_", ",", "_", "->>", "_", "->", "_")

	var colNames []string
	for _, col := range p.columns {
		colNames = append(colNames, strings.Trim(colMapper.Replace(col.column), "_"))
	}

	base := fmt.Sprintf("%s_%s_%s", prefix, rawTable, strings.Join(colNames, "_"))
	if len(base) <= postgresMaxIdentifierLength {
		return base
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(base))
	_, _ = h.Write([]byte(p.method))
	_, _ = h.Write([]byte(fmt.Sprintf("%v", p.unique)))
	_, _ = h.Write([]byte(p.where))
	_, _ = h.Write([]byte(p.schema))
	hashSuffix := fmt.Sprintf("%016x", h.Sum64())

	reservedLen := len(prefix) + 1 + len(hashSuffix) + 1
	maxPrefixLen := postgresMaxIdentifierLength - reservedLen

	if maxPrefixLen < 8 {
		return fmt.Sprintf("%s_%s", prefix, hashSuffix)
	}

	trimmed := base
	if len(base) > maxPrefixLen {
		trimmed = base[:maxPrefixLen]
	}
	return fmt.Sprintf("%s_%s", strings.TrimRight(trimmed, "_"), hashSuffix)
}

// ---------------------------------------------------------------------------------------------------------------------

// PostgresIndexManager manages synchronization between index definitions and the database.
type PostgresIndexManager struct {
	// db is the GORM database connection.
	db *gorm.DB

	// indexes is the list of indexes to manage.
	indexes []*PostgresIndex
}

// NewPostgresIndexManager initializes a manager with the provided DB and index list.
func NewPostgresIndexManager(db *gorm.DB, indexes []*PostgresIndex) *PostgresIndexManager {
	return &PostgresIndexManager{
		db:      db,
		indexes: indexes,
	}
}

// Create executes DDL for all managed indexes, handling concurrent creation safely.
func (m *PostgresIndexManager) Create() error {
	indexMap := make(map[string]*PostgresIndex)
	for _, idx := range m.indexes {
		indexMap[idx.ResolveIndexName()] = idx
	}

	for _, idx := range indexMap {
		sqlSlice, err := idx.ToSQL()
		if err != nil {
			return err
		}

		if idx.concurrently {
			for _, sql := range sqlSlice {
				if err := m.db.Exec(sql).Error; err != nil {
					return fmt.Errorf("concurrent creation error for %s: %w", idx.ResolveIndexName(), err)
				}
			}
		} else {
			err := m.db.Transaction(func(tx *gorm.DB) error {
				for _, sql := range sqlSlice {
					if err := tx.Exec(sql).Error; err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("transactional creation error for %s: %w", idx.ResolveIndexName(), err)
			}
		}
	}
	return nil
}

// CleanupGhost safely removes redundant indexes not present in the code definition.
func (m *PostgresIndexManager) CleanupGhost() error {
	type tableKey struct {
		schema string
		table  string
	}
	managedMap := make(map[tableKey]map[string]bool)

	for _, idx := range m.indexes {
		schema := idx.schema
		if schema == "" {
			schema = defaultPostgresSchema
		}

		tk := tableKey{
			schema: schema,
			table:  idx.ResolveTableName(),
		}
		if managedMap[tk] == nil {
			managedMap[tk] = make(map[string]bool)
		}
		managedMap[tk][idx.ResolveIndexName()] = true
	}

	for tk, managedNames := range managedMap {
		var existingNames []string

		rawSQL := `SELECT i.relname FROM pg_index x 
          JOIN pg_class c ON c.oid = x.indrelid 
          JOIN pg_class i ON i.oid = x.indexrelid 
          JOIN pg_namespace n ON n.oid = c.relnamespace 
          WHERE c.relname = ? AND x.indisprimary = false AND n.nspname = ?`

		if err := m.db.Raw(rawSQL, tk.table, tk.schema).Scan(&existingNames).Error; err != nil {
			return fmt.Errorf("ghost cleanup catalog query failed for %s.%s: %w", tk.schema, tk.table, err)
		}

		for _, dbName := range existingNames {
			if !managedNames[dbName] {
				p := &PostgresIndex{}
				dropPath := p.quoteIdent(dbName)
				if tk.schema != "" {
					dropPath = p.quoteIdent(tk.schema) + "." + dropPath
				}

				if err := m.db.Exec(fmt.Sprintf("DROP INDEX IF EXISTS %s", dropPath)).Error; err != nil {
					return fmt.Errorf("ghost cleanup: failed to drop index %s: %w", dbName, err)
				}
			}
		}
	}
	return nil
}
