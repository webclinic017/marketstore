package sqlparser_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alpacahq/marketstore/v4/executor"
	"github.com/alpacahq/marketstore/v4/sqlparser"
	"github.com/alpacahq/marketstore/v4/utils/functions"
	"github.com/alpacahq/marketstore/v4/utils/io"
	"github.com/alpacahq/marketstore/v4/utils/test"
)

func setup(t *testing.T, testName string,
) (tearDown func(), metadata *executor.InstanceMetadata) {
	t.Helper()

	rootDir, _ := os.MkdirTemp("", fmt.Sprintf("sqlparser_test-%s", testName))
	test.MakeDummyStockDir(rootDir, true, false)
	metadata, _, _, err := executor.NewInstanceSetup(rootDir, nil, nil, 5, executor.BackgroundSync(false))
	assert.Nil(t, err)

	return func() { test.CleanupDummyDataDir(rootDir) }, metadata
}

func TestSQLSelectParse(t *testing.T) {
	tearDown, _ := setup(t, "TestSQLSelectParse")
	defer tearDown()

	fmt.Printf("Running Presto Test Statements...")
	for _, tStmt := range testStatements {
		fmt.Printf("%d.", tStmt.n)
		parseAndPrintError(t, tStmt.stmt, tStmt.expectErr)
	}
	fmt.Printf("\n")

	fmt.Printf("Running Other Test Statements...")
	for _, tStmt := range otherTestStatements {
		fmt.Printf("%d.", tStmt.n)
		parseAndPrintError(t, tStmt.stmt, tStmt.expectErr)
	}
	fmt.Printf("\n")
}

func TestSQLSelect(t *testing.T) {
	tearDown, _ := setup(t, "TestSQLSelect")
	defer tearDown()

	stmt := "SELECT dibble JOIN;" // Should err out
	_, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, true, stmt)

	stmt = "SELECT Epoch, Open, High, Low, Close from `EURUSD/1Min/OHLC` WHERE Epoch BETWEEN '2000-01-01' AND '2002-01-01';"
	// stmt = "SELECT Epoch, Open, High, Low, Close from `EURUSD/1Min/OHLC` WHERE Epoch BETWEEN '2016-01-01' AND '2017-01-01';"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)

	/*
		stmt = "INSERT INTO `AAPL/1Min/OHLC` SELECT tickcandler(a,b,c) FROM `UVXY/1Min/TICKS`;"
		queryTree, err = BuildQueryTree(stmt)
		evalAndPrint(c, err, false, stmt)
		PrintExplain(queryTree, stmt)

		stmt = "CREATE VIEW candle5Min AS SELECT tickcandler(a,b,c) FROM `UVXY/1Min/TICKS`;"
		queryTree, err = BuildQueryTree(stmt)
		evalAndPrint(c, err, false, stmt)
		PrintExplain(queryTree, stmt)
	*/

	_ = queryTree
}

type Visitor struct {
	sqlparser.BaseSQLQueryTreeVisitor
}

func NewVisitor() *Visitor {
	return new(Visitor)
}

func (this *Visitor) Visit(tree sqlparser.IMSTree) interface{} {
	return tree.Accept(this)
}

func (this *Visitor) VisitStatementsParse(ctx *sqlparser.StatementsParse) interface{} {
	fmt.Println("Visiting SP")
	return ctx.GetChild(0)
}

func (this *Visitor) VisitStatementParse(ctx *sqlparser.StatementParse) interface{} {
	retval := 20202020
	fmt.Println("Visiting RP: ", retval)
	return retval
}

func TestVisitor(t *testing.T) {
	tearDown, _ := setup(t, "TestVisitor")
	defer tearDown()

	stmt := "INSERT INTO `AAPL/1Min/OHLC` SELECT tickcandler(a,b,c) FROM `UVXY/1Min/TICKS`;"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	v := NewVisitor()
	result := v.Visit(queryTree)
	fmt.Println("Result: ", result)
}

func TestExecutableStatement(t *testing.T) {
	tearDown, metadata := setup(t, "TestExecutableStatement")
	defer tearDown()
	aggRunner := sqlparser.NewAggRunner(nil)

	stmt := "SELECT Epoch, Open, High, Low, Close from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	// PrintExplain(queryTree, stmt)
	es, err := sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err := es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 29)

	stmt = "SELECT Epoch, Open, High, Low, Close from `AAPL/1Min/OHLCV` WHERE Epoch > '2000-01-05-12:30' AND Epoch < '2000-01-05-13:00';"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 29)

	// Impossible predicate, should return 0 results successfully
	stmt = "SELECT Epoch, Open, High, Low, Close from `AAPL/1Min/OHLCV` WHERE Epoch < '2000-01-05-12:30' AND Epoch > '2000-01-05-13:00';"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 0)
	assert.Nil(t, err)

	// Nested predicate
	stmt = "SELECT Epoch, Open, High, Low, Close from `AAPL/1Min/OHLCV` WHERE Open > 10.234 AND (Epoch > '2000-01-05-12:30' AND Epoch < '2000-01-05-13:00');"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	// PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 0)
	assert.Nil(t, err)

	// SELECT *
	stmt = "SELECT * from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	// PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 29)
	assert.Nil(t, err)

	stmt = "INSERT INTO `AAPL/5Min/OHLCV` SELECT * from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 1)
	assert.Nil(t, err)

	stmt = "select count(*) from `AAPL/1Min/OHLCV` where Epoch < 946684800;" // Should return 0 rows
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Nil(t, err)
	assert.Equal(t, cs.Len(), 0)
}

func TestStatementErrors(t *testing.T) {
	tearDown, metadata := setup(t, "TestStatementErrors")
	defer tearDown()
	aggRunner := sqlparser.NewAggRunner(nil)

	stmt := "select * from `fooble`;"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	es, err := sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err := es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, true, stmt)
	_ = cs
}

func TestInsertInto(t *testing.T) {
	tearDown, metadata := setup(t, "TestInsertInto")
	defer tearDown()
	aggRunner := sqlparser.NewAggRunner(nil)

	stmt := "INSERT INTO `AAPL/5Min/OHLCV` SELECT * from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	// PrintExplain(queryTree, stmt)
	es, err := sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err := es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 1)
}

func TestAggregation(t *testing.T) {
	tearDown, metadata := setup(t, "TestAggregation")
	defer tearDown()
	aggRunner := sqlparser.NewDefaultAggRunner(metadata.CatalogDir)

	cs := makeTestCS()
	epoch, ok := cs.GetColumn("Epoch").([]int64)
	assert.True(t, ok)
	one, ok := cs.GetColumn("One").([]float32)
	assert.True(t, ok)
	fmt.Println("Epoch	One")
	for i := range epoch {
		t := time.Unix(epoch[i], 0).UTC()
		fmt.Printf("%v\t%v\n", t, one[i])
	}

	agg := aggRunner.GetFunc("blargle") // aggregator not found
	assert.Nil(t, agg)

	agg = aggRunner.GetFunc(strings.ToLower("TickCandler"))
	assert.NotNil(t, agg)
	argMap := functions.NewArgumentMap(agg.GetRequiredArgs(), agg.GetOptionalArgs()...)
	dsPrice := io.DataShape{Name: "One", Type: io.FLOAT32}
	argMap.MapRequiredColumn("CandlePrice", dsPrice)
	tickCandler, _ := agg.New(argMap, "1Min")
	assert.NotNil(t, tickCandler)
	result, _ := tickCandler.Accum(io.TimeBucketKey{}, argMap, cs)
	r_epoch := result.GetColumn("Epoch").([]int64)
	r_open := result.GetColumn("Open").([]float32)
	r_high := result.GetColumn("High").([]float32)
	r_low := result.GetColumn("Low").([]float32)
	r_close := result.GetColumn("Close").([]float32)

	for i, tt := range r_epoch {
		fmt.Printf("Candle[%v] = Open:%f, High:%f, Low:%f, Close:%f\n",
			time.Unix(tt, 0).UTC(),
			r_open[i], r_high[i], r_low[i], r_close[i],
		)
	}
	assert.Equal(t, result.Len(), 2)
	assert.True(t, reflect.DeepEqual(r_epoch, []int64{1480586400, 1480586460}))
	assert.True(t, reflect.DeepEqual(r_open, []float32{1, 4}))
	assert.True(t, reflect.DeepEqual(r_high, []float32{3, 5}))
	assert.True(t, reflect.DeepEqual(r_low, []float32{1, 4}))
	assert.True(t, reflect.DeepEqual(r_close, []float32{3, 5}))

	stmt := "SELECT TickCandler('1Min', Open)  from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err := sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 29)

	stmt = "SELECT TickCandler('5Min', Open)  from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	assert.Equal(t, cs.Len(), 6)
	// fmt.Println(cs)
}

func TestCount(t *testing.T) {
	tearDown, metadata := setup(t, "TestCount")
	defer tearDown()
	aggRunner := sqlparser.NewDefaultAggRunner(metadata.CatalogDir)

	cs := makeTestCS()
	agg := aggRunner.GetFunc("count")
	argMap := functions.NewArgumentMap(agg.GetRequiredArgs(), agg.GetOptionalArgs()...)
	argMap.MapRequiredColumn("*", io.DataShape{Name: "Epoch", Type: io.INT64})
	tickCandler, _ := agg.New(argMap)
	assert.NotNil(t, tickCandler)
	result, _ := tickCandler.Accum(io.TimeBucketKey{}, argMap, cs)
	count, ok := result.GetColumn("Count").([]int64)
	assert.True(t, ok)
	assert.Equal(t, count[0], int64(5))

	// stmt := "SELECT count(*) from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	// stmt := "SELECT count(*) from (select tickcandler('1Min',Open) `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00');"
	stmt := "SELECT count(*) from `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00';"
	queryTree, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err := sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	count, ok = cs.GetColumn("Count").([]int64)
	assert.True(t, ok)
	assert.Equal(t, count[0], int64(29))

	/*
		Subselect
	*/
	stmt = "SELECT count(*) from (select * from `AAPL/1Min/OHLCV`);"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	count, ok = cs.GetColumn("Count").([]int64)
	assert.True(t, ok)
	assert.Equal(t, count[0], int64(1578240))

	stmt = "SELECT count(*) from (SELECT count(*) from (select * from `AAPL/1Min/OHLCV`));"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	T_PrintExplain(queryTree, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	cs, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, false, stmt)
	count, ok = cs.GetColumn("Count").([]int64)
	assert.True(t, ok)
	assert.Equal(t, count[0], int64(1))

	/*
		Subquery error handling
	*/
	stmt = "SELECT count(*) from (select tickcandler('1Min',Open) `AAPL/1Min/OHLCV` WHERE Epoch BETWEEN '2000-01-05-12:30' AND '2000-01-05-13:00');"
	queryTree, err = sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, false, stmt)
	es, err = sqlparser.NewExecutableStatement(queryTree)
	evalAndPrint(t, err, false, stmt)
	_, err = es.Materialize(aggRunner, metadata.CatalogDir)
	evalAndPrint(t, err, true, stmt)
}

/*
Utility functions.
*/
type TestStmt struct {
	n         int
	stmt      string
	expectErr bool
}

var testStatements = []TestStmt{
	{n: 1, stmt: "SELECT 123.456E7 FROM DUAL;", expectErr: false},
	{n: 2, stmt: "SELECT 123 INTERSECT DISTINCT SELECT 123 INTERSECT ALL SELECT 123;", expectErr: false},
	{n: 3, stmt: "SELECT 123 UNION DISTINCT SELECT 123 UNION ALL SELECT 123;", expectErr: false},
	{n: 4, stmt: "SELECT * FROM (VALUES (1, '1'), (2, '2')) LIMIT 100;", expectErr: false},
	{n: 5, stmt: "VALUES ('a', 1, 2.2), ('b', 2, 3.3);", expectErr: false},
	{n: 6, stmt: "SELECT * FROM (VALUES ('a', 1, 2.2), ('b', 2, 3.3));", expectErr: false},
	//{n: 7, stmt: "SET SESSION foo = 'bar';", expectErr: false},
	//{n: 8, stmt: "SET SESSION foo.bar = 'baz';", expectErr: false},
	//{n: 9, stmt: "SET SESSION foo.bar.boo = 'baz';", expectErr: false},
	//{n: 10, stmt: "SET SESSION foo.bar = 'ban' || 'ana';", expectErr: false},
	//{n: 11, stmt: "RESET SESSION foo.bar;", expectErr: false},
	//{n: 12, stmt: "RESET SESSION foo;", expectErr: false},
	//{n: 13, stmt: "SHOW SESSION;", expectErr: false},
	//{n: 14, stmt: "SHOW CATALOGS;", expectErr: false},
	//{n: 15, stmt: "SHOW CATALOGS LIKE '%';", expectErr: false},
	//{n: 16, stmt: "SHOW SCHEMAS;", expectErr: false},
	//{n: 17, stmt: "SHOW SCHEMAS FROM foo;", expectErr: false},
	//{n: 18, stmt: "SHOW SCHEMAS IN foo LIKE '%';", expectErr: false},
	//{n: 19, stmt: "SHOW TABLES;", expectErr: false},
	//{n: 20, stmt: "SHOW TABLES FROM a;", expectErr: false},
	//{n: 21, stmt: "SHOW TABLES IN a LIKE '%';", expectErr: false},
	//{n: 22, stmt: "SHOW PARTITIONS FROM t;", expectErr: false},
	//{n: 23, stmt: "SHOW PARTITIONS FROM t WHERE x = 1;", expectErr: false},
	//{n: 24, stmt: "SHOW PARTITIONS FROM t WHERE x = 1 ORDER BY y;", expectErr: false},
	//{n: 25, stmt: "SHOW PARTITIONS FROM t WHERE x = 1 ORDER BY y LIMIT 10;", expectErr: false},
	//{n: 26, stmt: "SHOW PARTITIONS FROM t WHERE x = 1 ORDER BY y LIMIT 100;", expectErr: false},
	{n: 27, stmt: "SELECT substring('%s' FROM 2);", expectErr: false},
	{n: 28, stmt: "SELECT substring('%s' FROM 2 FOR 3);", expectErr: false},
	{n: 29, stmt: "SELECT substring('%s', 2);", expectErr: false},
	{n: 30, stmt: "SELECT substring('%s', 2, 3);", expectErr: false},
	{n: 31, stmt: "SELECT col1.f1, col2, col3.f1.f2.f3 FROM table1;", expectErr: false},
	{n: 32, stmt: "SELECT col1.f1[0], col2, col3[2].f2.f3, col4[4] FROM table1;", expectErr: false},
	{n: 33, stmt: "SELECT CAST(ROW(11, 12) AS ROW(COL0 INTEGER, COL1 INTEGER)).col0;", expectErr: false},
	{n: 34, stmt: "SELECT * FROM table1 ORDER BY a;", expectErr: false},
	{n: 35, stmt: "SELECT * FROM table1 GROUP BY a;", expectErr: false},
	{n: 36, stmt: "SELECT * FROM table1 GROUP BY a, b;", expectErr: false},
	{n: 37, stmt: "SELECT * FROM table1 GROUP BY ();", expectErr: false},
	{n: 38, stmt: "SELECT * FROM table1 GROUP BY GROUPING SETS (a);", expectErr: false},
	{n: 39, stmt: "SELECT * FROM table1 GROUP BY ALL GROUPING SETS ((a, b), (a), ()), CUBE (c), ROLLUP (d);", expectErr: false},
	{n: 40, stmt: "SELECT * FROM table1 GROUP BY DISTINCT GROUPING SETS ((a, b), (a), ()), CUBE (c), ROLLUP (d);", expectErr: false},
	//	"CREATE SCHEMA test;", expectErr: false},
	//	"CREATE SCHEMA IF NOT EXISTS test;", expectErr: false},
	//	"CREATE SCHEMA test WITH (a = 'apple', b = 123);", expectErr: false},
	//	"DROP SCHEMA test;", expectErr: false},
	//	"DROP SCHEMA test CASCADE;", expectErr: false},
	//	"DROP SCHEMA IF EXISTS test;", expectErr: false},
	//	"DROP SCHEMA IF EXISTS test RESTRICT;", expectErr: false},
	//	"ALTER SCHEMA foo RENAME TO bar;", expectErr: false},
	//	"ALTER SCHEMA foo.bar RENAME TO baz;", expectErr: false},
	//{n: 41, stmt: "CREATE TABLE foo (a VARCHAR, b BIGINT COMMENT 'hello world');", expectErr: false},
	//{n: 42, stmt: "CREATE TABLE IF NOT EXISTS bar (c TIMESTAMP);", expectErr: false},
	//{n: 43, stmt: "CREATE TABLE IF NOT EXISTS bar (LIKE like_table);", expectErr: false},
	//{n: 44, stmt: "CREATE TABLE IF NOT EXISTS bar (c TIMESTAMP, LIKE like_table);", expectErr: false},
	//{n: 45, stmt: "CREATE TABLE IF NOT EXISTS bar (c TIMESTAMP, LIKE like_table, d DATE);", expectErr: false},
	//{n: 46, stmt: "CREATE TABLE IF NOT EXISTS bar (LIKE like_table INCLUDING PROPERTIES);", expectErr: false},
	//{n: 47, stmt: "CREATE TABLE IF NOT EXISTS bar (c TIMESTAMP, LIKE like_table EXCLUDING PROPERTIES);", expectErr: false},
	//{n: 48, stmt: "CREATE TABLE foo AS SELECT * FROM t;", expectErr: false},
	//{n: 49, stmt: "CREATE TABLE IF NOT EXISTS foo AS SELECT * FROM t;", expectErr: false},
	//	{n: 50, stmt: "CREATE TABLE foo AS SELECT * FROM t WITH NO DATA;", expectErr: false},
	//{n: 51, stmt: "CREATE TABLE foo " +
	//	"WITH ( string = 'bar', long = 42, computed = 'ban' || 'ana', a  = ARRAY[ 'v1', 'v2' ] ) " +
	//	"AS SELECT * FROM t;", expectErr: false},
	//	{n: 52, stmt: "CREATE TABLE foo " +
	//		"WITH ( string = 'bar', long = 42, computed = 'ban' || 'ana', a  = ARRAY[ 'v1', 'v2' ] ) " +
	//		"AS SELECT * FROM t WITH NO DATA;", expectErr: true},
	//{n: 53, stmt: "DROP TABLE a;", expectErr: false},
	//{n: 54, stmt: "DROP TABLE a.b;", expectErr: false},
	//{n: 55, stmt: "DROP TABLE a.b.c;", expectErr: false},
	//{n: 56, stmt: "DROP TABLE IF EXISTS a;", expectErr: false},
	//{n: 57, stmt: "DROP TABLE IF EXISTS a.b;", expectErr: false},
	//{n: 58, stmt: "DROP TABLE IF EXISTS a.b.c;", expectErr: false},
	//{n: 59, stmt: "DROP VIEW a;", expectErr: false},
	//{n: 60, stmt: "DROP VIEW a.b;", expectErr: false},
	//{n: 61, stmt: "DROP VIEW a.b.c;", expectErr: false},
	//{n: 62, stmt: "DROP VIEW IF EXISTS a;", expectErr: false},
	//{n: 63, stmt: "DROP VIEW IF EXISTS a.b;", expectErr: false},
	//{n: 64, stmt: "DROP VIEW IF EXISTS a.b.c;", expectErr: false},
	{n: 65, stmt: "INSERT INTO a SELECT * FROM t;", expectErr: false},
	{n: 66, stmt: "INSERT INTO a (c1, c2) SELECT * FROM t;", expectErr: false},
	//{n: 67, stmt: "DELETE FROM t;", expectErr: false},
	//{n: 68, stmt: "DELETE FROM t WHERE a = b;", expectErr: false},
	//{n: 69, stmt: "ALTER TABLE a RENAME TO b;", expectErr: false},
	//{n: 70, stmt: "ALTER TABLE foo.t RENAME COLUMN a TO b;", expectErr: false},
	//{n: 71, stmt: "ALTER TABLE foo.t ADD COLUMN c bigint;", expectErr: false},
	//{n: 72, stmt: "CREATE VIEW a AS SELECT * FROM t;", expectErr: false},
	//{n: 73, stmt: "CREATE OR REPLACE VIEW a AS SELECT * FROM t;", expectErr: false},
	//{n: 74, stmt: "GRANT INSERT, DELETE ON t TO u;", expectErr: false},
	//{n: 75, stmt: "GRANT SELECT ON t TO PUBLIC WITH GRANT OPTION;", expectErr: false},
	//{n: 76, stmt: "GRANT ALL PRIVILEGES ON t TO u;", expectErr: false},
	//{n: 77, stmt: "GRANT taco ON t TO PUBLIC WITH GRANT OPTION;", expectErr: false},
	//{n: 78, stmt: "REVOKE INSERT, DELETE ON t FROM u;", expectErr: false},
	//{n: 79, stmt: "REVOKE GRANT OPTION FOR SELECT ON t FROM PUBLIC;", expectErr: false},
	//{n: 80, stmt: "REVOKE ALL PRIVILEGES ON TABLE t FROM u;", expectErr: false},
	//{n: 81, stmt: "REVOKE taco ON TABLE t FROM u;", expectErr: false},
	{n: 82, stmt: "WITH a (t, u) AS (SELECT * FROM x), b AS (SELECT * FROM y) TABLE z;", expectErr: false},
	{n: 83, stmt: "WITH RECURSIVE a AS (SELECT * FROM x) TABLE y;", expectErr: false},
	{n: 84, stmt: "SELECT * FROM a, b;", expectErr: false},
	{n: 85, stmt: "EXPLAIN SELECT * FROM t;", expectErr: false},
	{n: 86, stmt: "EXPLAIN (TYPE LOGICAL) SELECT * FROM t;", expectErr: false},
	{n: 87, stmt: "EXPLAIN (TYPE LOGICAL, FORMAT TEXT) SELECT * FROM t;", expectErr: false},
	{n: 88, stmt: "EXPLAIN ANALYZE SELECT * FROM t;", expectErr: false},
	{n: 89, stmt: "SELECT * FROM a CROSS JOIN b LEFT JOIN c ON true;", expectErr: false},
	{n: 90, stmt: "SELECT * FROM a CROSS JOIN b NATURAL JOIN c CROSS JOIN d NATURAL JOIN e;", expectErr: false},
	{n: 91, stmt: "SELECT * FROM t CROSS JOIN UNNEST(a);", expectErr: false},
	{n: 92, stmt: "SELECT * FROM t CROSS JOIN UNNEST(a) WITH ORDINALITY;", expectErr: false},
	//{n: 93, stmt: "START TRANSACTION;", expectErr: false},
	//{n: 94, stmt: "START TRANSACTION ISOLATION LEVEL READ UNCOMMITTED;", expectErr: false},
	//{n: 95, stmt: "START TRANSACTION ISOLATION LEVEL READ COMMITTED;", expectErr: false},
	//{n: 96, stmt: "START TRANSACTION ISOLATION LEVEL REPEATABLE READ;", expectErr: false},
	//{n: 97, stmt: "START TRANSACTION ISOLATION LEVEL SERIALIZABLE;", expectErr: false},
	//{n: 98, stmt: "START TRANSACTION READ ONLY;", expectErr: false},
	//{n: 99, stmt: "START TRANSACTION READ WRITE;", expectErr: false},
	//{n: 100, stmt: "START TRANSACTION ISOLATION LEVEL READ COMMITTED, READ ONLY;", expectErr: false},
	//{n: 101, stmt: "START TRANSACTION READ ONLY, ISOLATION LEVEL READ COMMITTED;", expectErr: false},
	//{n: 102, stmt: "START TRANSACTION READ WRITE, ISOLATION LEVEL SERIALIZABLE;", expectErr: false},
	//{n: 103, stmt: "COMMIT;", expectErr: false},
	//{n: 104, stmt: "COMMIT WORK;", expectErr: false},
	//{n: 105, stmt: "ROLLBACK;", expectErr: false},
	//{n: 106, stmt: "ROLLBACK WORK;", expectErr: false},
	{n: 107, stmt: "SELECT timestamp '2012-10-31 01:00 UTC' AT TIME ZONE 'America/Los_Angeles';", expectErr: false},
	{n: 108, stmt: "SELECT zone FROM t;", expectErr: false},
	{n: 109, stmt: "SELECT INCLUDING, EXCLUDING, PROPERTIES FROM t;", expectErr: false},
	{n: 110, stmt: "SELECT ALL, SOME, ANY FROM t;", expectErr: false},
	//{n: 111, stmt: "CALL foo();", expectErr: false},
	//{n: 112, stmt: "CALL foo(123, a => 1, b => 'go', 456);", expectErr: false},
	//{n: 113, stmt: "PREPARE myquery FROM select * from foo;", expectErr: false},
	//{n: 114, stmt: "PREPARE myquery FROM SELECT ?, ? FROM foo;", expectErr: false},
	//{n: 115, stmt: "DEALLOCATE PREPARE myquery;", expectErr: false},
	//{n: 116, stmt: "EXECUTE myquery;", expectErr: false},
	//{n: 117, stmt: "EXECUTE myquery USING 1, 'abc', ARRAY ['hello'];", expectErr: false},
	{n: 118, stmt: "SELECT EXISTS(SELECT 1);", expectErr: false},
	//{n: 119, stmt: "DESCRIBE OUTPUT myquery;", expectErr: false},
	//{n: 120, stmt: "DESCRIBE INPUT myquery;", expectErr: false},
	{n: 121, stmt: "SELECT SUM(x) FILTER (WHERE x > 4);", expectErr: false},
}

var otherTestStatements = []TestStmt{
	{n: 1, stmt: "SELECT mytable;", expectErr: false},
	{n: 2, stmt: "SELECT * from mytable;", expectErr: false},

	// Order By
	{n: 3, stmt: "SELECT * from mytable order by a desc;", expectErr: false},
	{n: 4, stmt: "SELECT * from mytable order by a desc nulls first;", expectErr: false},
	{n: 5, stmt: "SELECT * from mytable order by a desc, b asc nulls first;", expectErr: false},

	// Where Predicates
	{n: 6, stmt: "SELECT * from mytable where a < 2012-10-01;", expectErr: false},
	{n: 7, stmt: "SELECT * from mytable where a > b;", expectErr: false},
	{n: 8, stmt: "SELECT * from mytable where a between 2012-10-01 and 2013-11-02;", expectErr: false},
	{n: 9, stmt: "SELECT * from mytable where a > ALL ( select b from c );", expectErr: false}, // TODO: Subquery not supported, implied Join
	{n: 10, stmt: "SELECT * from mytable where a in (1, 2, 'Apples');", expectErr: false},
	{n: 11, stmt: "SELECT * from mytable where a in (select a from b);", expectErr: false}, // TODO: Subquery not supported, implied join
	{n: 12, stmt: "SELECT * from mytable where a like 'abc%fine' escape '+' ;", expectErr: false},
	{n: 13, stmt: "SELECT * from mytable where a is NULL;", expectErr: false},
	{n: 14, stmt: "SELECT * from mytable where a is distinct from NULL;", expectErr: false},

	// SELECT list
	{n: 15, stmt: "SELECT a AS b from mytable;", expectErr: false},
	{n: 16, stmt: "SELECT a AS b, c AS d, d from mytable;", expectErr: false},
	{n: 17, stmt: "SELECT a from AAPL.`1Min`.OHLCV;", expectErr: false},
	{n: 18, stmt: "SELECT a from \"AAPL/1Min/OHLCV\";", expectErr: false},
	{n: 19, stmt: "SELECT a from (select b from (select c from (select d from T)));", expectErr: false}, // TODO: JOIN

	// JOIN
	{n: 20, stmt: "SELECT T1.a, T2.b from T1, T2 where T1.a = T2.b;", expectErr: false}, // TODO: JOIN
}

func T_PrintExplain(mtree sqlparser.IMSTree, stmt string) {
	result := sqlparser.Explain(mtree)
	printFiller := func(num int) {
		for i := 0; i < num; i++ {
			fmt.Printf("=")
		}
		fmt.Printf("\n")
	}
	fmt.Printf("\n")
	printFiller(len(stmt))
	fmt.Printf("%s\n", stmt)
	printFiller(len(stmt))
	for i := len(result) - 1; i >= 0; i-- {
		fmt.Println(result[i])
	}
}

func evalAndPrint(t *testing.T, err error, shouldErr bool, msg ...string) {
	t.Helper()

	if err != nil {
		if len(msg) == 0 { // Default is to print only the error
			fmt.Printf("\n%s\n", err.Error())
		} else {
			if msg[0] != "" {
				fmt.Printf("\n%s\n%s\n", err.Error(), msg[0])
			}
		}
	}
	assert.Equal(t, err != nil, shouldErr)
}

func parseAndPrintError(t *testing.T, stmt string, shouldErr bool) {
	t.Helper()

	_, err := sqlparser.BuildQueryTree(stmt)
	evalAndPrint(t, err, shouldErr, stmt)
}

func makeTestCS() (csA *io.ColumnSeries) {
	t1 := time.Date(2016, time.December, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(10 * time.Second)
	t3 := t2.Add(40 * time.Second)
	t4 := t3.Add(30 * time.Second)
	t5 := t4.Add(20 * time.Second)
	col1 := []float32{1, 2, 3, 4, 5}
	col2 := []float64{1, 2, 3, 4, 5}
	col3 := []int32{1, 2, 3, 4, 5}
	col4 := []int64{t1.Unix(), t2.Unix(), t3.Unix(), t4.Unix(), t5.Unix()}
	col5 := []byte{1, 2, 3, 4, 5}
	csA = io.NewColumnSeries()
	csA.AddColumn("Epoch", col4)
	csA.AddColumn("One", col1)
	csA.AddColumn("Two", col2)
	csA.AddColumn("Three", col3)
	csA.AddColumn("Four", col4)
	csA.AddColumn("Five", col5)
	return csA
}
