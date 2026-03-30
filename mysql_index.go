package xgorm

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------------------------------------------------

const (
	// mysqlMaxIdentifierBytes is the 64-byte limit for MySQL identifiers (table/index names).
	mysqlMaxIdentifierBytes = 64

	// mysqlMaxColumnsPerIndex is the maximum number of columns allowed in a composite index by InnoDB.
	mysqlMaxColumnsPerIndex = 16

	// innodbMaxKeyPayloadBytes is the 3072-byte limit for InnoDB index keys (Dynamic/Compressed formats).
	// Note: For REDUNDANT or COMPACT row formats, this limit is significantly lower (767 bytes).
	innodbMaxKeyPayloadBytes = 3072
)

// ---------------------------------------------------------------------------------------------------------------------

var (
	// mysqlReSimpleIdent validates if a string is a standard, non-quoted MySQL identifier.
	mysqlReSimpleIdent = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

	// mysqlVersionRegex extracts Major.Minor.Patch from MySQL or MariaDB version strings.
	mysqlVersionRegex = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.?([0-9]+)?`)
)

// MySQLVersion represents a structured semver for database compatibility checks.
type MySQLVersion struct {
	Major int
	Minor int
	Patch int
}

// NewMySQLVersion parses a raw version string (e.g., "8.0.32", "10.6.15-MariaDB").
func NewMySQLVersion(v string) MySQLVersion {
	matches := mysqlVersionRegex.FindStringSubmatch(v)
	if len(matches) < 3 {
		return MySQLVersion{Major: 5, Minor: 7, Patch: 0}
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch := 0
	if len(matches) > 3 && matches[3] != "" {
		patch, _ = strconv.Atoi(matches[3])
	}
	return MySQLVersion{Major: major, Minor: minor, Patch: patch}
}

// ---------------------------------------------------------------------------------------------------------------------

type MysqlIndexMethod string

const (
	MysqlIndexBTree    MysqlIndexMethod = "BTREE"
	MysqlIndexHash     MysqlIndexMethod = "HASH"
	MysqlIndexFullText MysqlIndexMethod = "FULLTEXT"
	MysqlIndexSpatial  MysqlIndexMethod = "SPATIAL"
)

// ---------------------------------------------------------------------------------------------------------------------

type MysqlAlgorithm string

const (
	MysqlAlgorithmDefault MysqlAlgorithm = "DEFAULT"
	MysqlAlgorithmInplace MysqlAlgorithm = "INPLACE"
	MysqlAlgorithmCopy    MysqlAlgorithm = "COPY"
	MysqlAlgorithmInstant MysqlAlgorithm = "INSTANT"
)

// ---------------------------------------------------------------------------------------------------------------------

type MysqlLock string

const (
	MysqlLockDefault   MysqlLock = "DEFAULT"
	MysqlLockNone      MysqlLock = "NONE"
	MysqlLockShared    MysqlLock = "SHARED"
	MysqlLockExclusive MysqlLock = "EXCLUSIVE"
)

// ---------------------------------------------------------------------------------------------------------------------

// MysqlIndexColumn defines the properties of an individual column within an index.
type MysqlIndexColumn struct {
	column       string // Name of the column or the functional expression.
	length       int    // Prefix length for BLOB/TEXT or truncated VARCHAR columns.
	order        string // Sorting order: "ASC" or "DESC".
	isExpression bool   // True if the column is a functional expression (MySQL 8.0+).
}

// NewMysqlIndexColumn initializes a column and auto-detects if it's a functional expression.
func NewMysqlIndexColumn(col string) *MysqlIndexColumn {
	col = strings.TrimSpace(col)
	return &MysqlIndexColumn{
		column:       col,
		isExpression: !mysqlReSimpleIdent.MatchString(col),
	}
}

// Length sets a prefix length for the index column.
func (c *MysqlIndexColumn) Length(l int) *MysqlIndexColumn { c.length = l; return c }

// Desc sets the index column sorting to Descending.
func (c *MysqlIndexColumn) Desc() *MysqlIndexColumn { c.order = "DESC"; return c }

// ---------------------------------------------------------------------------------------------------------------------

// MysqlIndex encapsulates all metadata required to plan and generate a MySQL index.
type MysqlIndex struct {
	table         string              // Target table name.
	model         any                 // GORM model used to resolve table name if table is empty.
	database      string              // Target schema/database name.
	name          string              // Explicit index name (optional; auto-generated if empty).
	method        MysqlIndexMethod    // Indexing method (e.g., BTREE, HASH).
	columns       []*MysqlIndexColumn // Ordered list of columns participating in the index.
	unique        bool                // Whether to enforce a UNIQUE constraint.
	engine        string              // Storage engine (defaults to INNODB).
	algorithm     MysqlAlgorithm      // Online DDL ALGORITHM (INPLACE, COPY, etc.).
	lock          MysqlLock           // Online DDL LOCK (NONE, SHARED, etc.).
	comment       string              // Index-level comment.
	invisible     bool                // Whether the index is hidden from the optimizer (8.0.11+).
	serverVersion MySQLVersion        // Server version for feature compatibility gating.
	strictKeyLen  bool                // If true, validates 3072-byte key limit during generation.
}

// NewMysqlIndex returns a pre-configured index builder for InnoDB.
func NewMysqlIndex() *MysqlIndex {
	return &MysqlIndex{
		method:    MysqlIndexBTree,
		algorithm: MysqlAlgorithmDefault,
		lock:      MysqlLockDefault,
		engine:    "INNODB",
	}
}

func (m *MysqlIndex) Table(t string) *MysqlIndex {
	m.table = t
	return m
}

func (m *MysqlIndex) Model(v any) *MysqlIndex {
	m.model = v
	return m
}

func (m *MysqlIndex) Database(db string) *MysqlIndex {
	m.database = db
	return m
}

func (m *MysqlIndex) Name(n string) *MysqlIndex {
	m.name = n
	return m
}

func (m *MysqlIndex) Unique(u bool) *MysqlIndex {
	m.unique = u
	return m
}

func (m *MysqlIndex) Engine(e string) *MysqlIndex {
	m.engine = strings.ToUpper(e)
	return m
}

func (m *MysqlIndex) Method(mt MysqlIndexMethod) *MysqlIndex {
	m.method = mt
	return m
}

func (m *MysqlIndex) Comment(c string) *MysqlIndex {
	m.comment = c
	return m
}

func (m *MysqlIndex) Invisible(v bool) *MysqlIndex {
	m.invisible = v
	return m
}

func (m *MysqlIndex) StrictKeyLen(s bool) *MysqlIndex {
	m.strictKeyLen = s
	return m
}

// Online configures the Online DDL options (Algorithm and Lock).
func (m *MysqlIndex) Online(a MysqlAlgorithm, l MysqlLock) *MysqlIndex {
	m.algorithm = a
	m.lock = l
	return m
}

// Fields adds columns by name to the index.
func (m *MysqlIndex) Fields(f ...string) *MysqlIndex {
	for _, v := range f {
		m.columns = append(m.columns, NewMysqlIndexColumn(v))
	}
	return m
}

// Columns adds fully configured MysqlIndexColumn instances to the index.
func (m *MysqlIndex) Columns(cols ...*MysqlIndexColumn) *MysqlIndex {
	m.columns = append(m.columns, cols...)
	return m
}

// ToSQL generates the final "CREATE [UNIQUE] INDEX..." DDL statement.
// It performs validation on engine compatibility, column counts, and MySQL versions.
func (m *MysqlIndex) ToSQL(db *gorm.DB) (string, error) {
	if m.method == MysqlIndexHash && m.engine != "MEMORY" && m.engine != "NDB" {
		return "", fmt.Errorf("HASH index requires MEMORY/NDB engine, current: %s", m.engine)
	}

	if len(m.columns) == 0 || len(m.columns) > mysqlMaxColumnsPerIndex {
		return "", fmt.Errorf("column count %d is outside allowed range [1, %d]", len(m.columns), mysqlMaxColumnsPerIndex)
	}

	table := m.ResolveTableName(db)
	var buf strings.Builder
	buf.WriteString("CREATE")

	if m.unique {
		if m.method == MysqlIndexFullText || m.method == MysqlIndexSpatial {
			return "", fmt.Errorf("%s index cannot be UNIQUE", m.method)
		}
		buf.WriteString(" UNIQUE")
	} else {
		switch m.method {
		case MysqlIndexFullText:
			buf.WriteString(" FULLTEXT")
		case MysqlIndexSpatial:
			buf.WriteString(" SPATIAL")
		}
	}

	buf.WriteString(" INDEX ")
	idxName := m.ResolveIndexName(db)

	fullPath := m.quoteIdent(table)
	if m.database != "" {
		fullPath = m.quoteIdent(m.database) + "." + fullPath
	}
	buf.WriteString(fmt.Sprintf("%s ON %s ", m.quoteIdent(idxName), fullPath))

	if m.method == MysqlIndexHash {
		buf.WriteString("USING HASH ")
	}

	// Utilize GORM Statement to map field names to DB columns
	stmt := &gorm.Statement{DB: db}
	if m.model != nil {
		_ = stmt.Parse(m.model)
	}

	var colSpecs []string
	estimatedBytes := 0
	for _, c := range m.columns {
		if c.isExpression && m.serverVersion.Major < 8 {
			return "", fmt.Errorf("functional index requires MySQL 8.0+, current: %d.%d", m.serverVersion.Major, m.serverVersion.Minor)
		}

		columnName := c.column
		if !c.isExpression && stmt.Schema != nil {
			if f, ok := stmt.Schema.FieldsByName[c.column]; ok {
				columnName = f.DBName
			}
		}

		order := "ASC"
		if strings.ToUpper(c.order) == "DESC" {
			order = "DESC"
		}

		spec := ""
		if c.isExpression {
			spec = fmt.Sprintf("(%s)", columnName)
		} else {
			spec = m.quoteIdent(columnName)
			if c.length > 0 {
				spec += fmt.Sprintf("(%d)", c.length)
				estimatedBytes += c.length
			} else {
				// Rough estimation for VARCHAR/CHAR keys (Assuming utf8mb4: 4 bytes/char)
				estimatedBytes += 255 * 4
			}
		}
		colSpecs = append(colSpecs, spec+" "+order)
	}

	if m.strictKeyLen && m.engine == "INNODB" && estimatedBytes > innodbMaxKeyPayloadBytes {
		return "", fmt.Errorf("estimated key length %d exceeds InnoDB %d limit", estimatedBytes, innodbMaxKeyPayloadBytes)
	}

	buf.WriteString("(" + strings.Join(colSpecs, ", ") + ")")

	if m.comment != "" {
		buf.WriteString(" COMMENT '" + strings.ReplaceAll(m.comment, "'", "''") + "'")
	}

	if m.invisible && (m.serverVersion.Major > 8 || (m.serverVersion.Major == 8 && m.serverVersion.Minor >= 0)) {
		buf.WriteString(" INVISIBLE")
	}

	if m.algorithm != "" && m.algorithm != MysqlAlgorithmDefault {
		buf.WriteString(fmt.Sprintf(" ALGORITHM=%s", m.algorithm))
	}
	if m.lock != "" && m.lock != MysqlLockDefault {
		buf.WriteString(fmt.Sprintf(" LOCK=%s", m.lock))
	}

	buf.WriteString(";")
	return buf.String(), nil
}

// quoteIdent wraps identifiers in backticks and handles escaping.
func (m *MysqlIndex) quoteIdent(s string) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = "`" + strings.ReplaceAll(p, "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}

// ResolveIndexName generates a unique index name if not provided.
// Format: {prefix}_{table_and_cols}_{sha1_hash}
func (m *MysqlIndex) ResolveIndexName(db *gorm.DB) string {
	if m.name != "" {
		return m.name
	}
	prefix := "idx"
	if m.unique {
		prefix = "uk"
	}

	h := sha1.New()
	h.Write([]byte(m.database))
	h.Write([]byte(m.ResolveTableName(db)))
	readablePart := ""
	for _, c := range m.columns {
		h.Write([]byte(fmt.Sprintf("%s|%d|%s", c.column, c.length, c.order)))
		if !c.isExpression {
			readablePart += "_" + c.column
		}
	}
	h.Write([]byte(m.method))
	h.Write([]byte(fmt.Sprintf("%v", m.unique)))
	hashStr := fmt.Sprintf("%016x", h.Sum(nil)[:8])

	overhead := len(prefix) + 2 + len(hashStr) // prefix + __ + hash
	maxAvail := mysqlMaxIdentifierBytes - overhead

	rawBody := m.ResolveTableName(db) + readablePart
	bodyIdx := m.maxTableLenByBytes(rawBody, maxAvail)
	bodyPart := strings.TrimRight(rawBody[:bodyIdx], "_")

	if bodyPart == "" {
		bodyPart = "t"
	}

	return fmt.Sprintf("%s_%s_%s", prefix, bodyPart, hashStr)
}

// maxTableLenByBytes safely truncates a string to a byte limit without breaking UTF-8 runes.
func (m *MysqlIndex) maxTableLenByBytes(s string, limit int) int {
	if limit <= 0 {
		return 0
	}
	currBytes := 0
	for i, r := range s {
		rLen := utf8.RuneLen(r)
		if currBytes+rLen > limit {
			return i
		}
		currBytes += rLen
	}
	return len(s)
}

// ResolveTableName retrieves the table name from explicit setting or GORM model metadata.
func (m *MysqlIndex) ResolveTableName(db *gorm.DB) string {
	return getTableName(db, m.model, m.table)
}

// ---------------------------------------------------------------------------------------------------------------------

// MysqlIndexManager orchestrates the creation and pruning of database indexes.
type MysqlIndexManager struct {
	db              *gorm.DB
	indexes         []*MysqlIndex
	managedPrefixes []string // Prefixes used to identify automated indexes (e.g., "idx", "uk").
	whiteList       []string // Specific index names to never delete during cleanup.
}

// NewMysqlIndexManager initializes a manager with a GORM database instance.
func NewMysqlIndexManager(db *gorm.DB, idx []*MysqlIndex) *MysqlIndexManager {
	return &MysqlIndexManager{
		db:              db,
		indexes:         idx,
		managedPrefixes: []string{"idx", "uk"},
		whiteList:       []string{},
	}
}

// Prefixes updates the prefixes used for identifying managed indexes.
func (m *MysqlIndexManager) Prefixes(p ...string) *MysqlIndexManager {
	m.managedPrefixes = append(m.managedPrefixes, p...)
	return m
}

// WhiteList defines index names that should be ignored by the CleanupGhost process.
func (m *MysqlIndexManager) WhiteList(w ...string) *MysqlIndexManager {
	m.whiteList = append(m.whiteList, w...)
	return m
}

// getManagedRegex compiles the regex pattern used to scan for auto-generated indexes.
func (m *MysqlIndexManager) getManagedRegex() *regexp.Regexp {
	escaped := make([]string, 0, len(m.managedPrefixes))
	for _, p := range m.managedPrefixes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		escaped = append(escaped, regexp.QuoteMeta(p))
	}
	if len(escaped) == 0 {
		return regexp.MustCompile(`^$`)
	}
	pattern := fmt.Sprintf(`^(%s)_.*_[0-9a-f]{16}$`, strings.Join(escaped, "|"))
	return regexp.MustCompile(pattern)
}

// Create executes the index creation logic, ensuring duplicates are not attempted.
func (m *MysqlIndexManager) Create() error {
	var currentDB, vStr string
	if err := m.db.Raw("SELECT DATABASE()").Scan(&currentDB).Error; err != nil {
		return err
	}
	if err := m.db.Raw("SELECT VERSION()").Scan(&vStr).Error; err != nil {
		return err
	}
	version := NewMySQLVersion(vStr)

	for _, idx := range m.indexes {
		idx.serverVersion = version
		targetDB := idx.database
		if targetDB == "" {
			targetDB = currentDB
		}

		tableName := idx.ResolveTableName(m.db)
		indexName := idx.ResolveIndexName(m.db)

		var count int64
		checkSQL := `
          SELECT COUNT(*) 
          FROM INFORMATION_SCHEMA.STATISTICS 
            WHERE 
              TABLE_SCHEMA = ? 
              AND TABLE_NAME = ? 
              AND INDEX_NAME = ?
        `
		if err := m.db.Raw(checkSQL, targetDB, tableName, indexName).Scan(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}

		sql, err := idx.ToSQL(m.db)
		if err != nil {
			return err
		}
		if err = m.db.Exec(sql).Error; err != nil {
			// Ignore Error 1061: Duplicate key name (race condition check)
			var me *mysql.MySQLError
			if errors.As(err, &me) && me.Number == 1061 {
				continue
			}
			return fmt.Errorf("failed to create index %s: %w", indexName, err)
		}
	}
	return nil
}

// CleanupGhost drops indexes that exist in the database and match the managed pattern,
// but are no longer defined in the provided Go plan.
func (m *MysqlIndexManager) CleanupGhost() error {
	var currentDB string
	if err := m.db.Raw("SELECT DATABASE()").Scan(&currentDB).Error; err != nil {
		return err
	}
	managedPattern := m.getManagedRegex()

	type tk struct{ db, table string }
	managedMap := make(map[tk]map[string]bool)

	for _, idx := range m.indexes {
		db := idx.database
		if db == "" {
			db = currentDB
		}
		key := tk{db: db, table: idx.ResolveTableName(m.db)}
		if managedMap[key] == nil {
			managedMap[key] = make(map[string]bool)
		}
		managedMap[key][idx.ResolveIndexName(m.db)] = true
	}

	for key, managedNames := range managedMap {
		var dbNames []string
		query := `
          SELECT DISTINCT INDEX_NAME 
          FROM INFORMATION_SCHEMA.STATISTICS 
            WHERE 
                TABLE_SCHEMA = ? 
              AND TABLE_NAME = ? 
              AND INDEX_NAME != 'PRIMARY'
        `
		if err := m.db.Raw(query, key.db, key.table).Scan(&dbNames).Error; err != nil {
			return err
		}

		for _, name := range dbNames {
			// Skip if in white list
			isWhite := false
			for _, w := range m.whiteList {
				if strings.EqualFold(w, name) {
					isWhite = true
					break
				}
			}
			if isWhite {
				continue
			}

			// Drop if matches auto-generation pattern but is missing from current definitions
			if managedPattern.MatchString(strings.ToLower(name)) && !managedNames[name] {
				dropSQL := fmt.Sprintf("DROP INDEX %s ON %s.%s",
					(&MysqlIndex{}).quoteIdent(name),
					(&MysqlIndex{}).quoteIdent(key.db),
					(&MysqlIndex{}).quoteIdent(key.table))
				if err := m.db.Exec(dropSQL).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}
