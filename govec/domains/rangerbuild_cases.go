package domains

// The case tables of pkg/util/ranger/ranger_test.go (Go TiDB v8.5.8), copied verbatim as
// inputs of domain "rangerbuild" (rangerbuild.go); the expected strings are checked against
// the generated records (rbCheck).

import (
	"github.com/pingcap/tidb/pkg/types"
)

var _ = types.UnspecifiedLength

// from: pkg/util/ranger/ranger_test.go TestTableRange
var rbTableRange = []rbCase{
	{
		exprStr:     "a = 1",
		accessConds: "[eq(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[1,1]]",
	},
	{
		exprStr:     "1 = a",
		accessConds: "[eq(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[1,1]]",
	},
	{
		exprStr:     "a != 1",
		accessConds: "[ne(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (1,+inf]]",
	},
	{
		exprStr:     "1 != a",
		accessConds: "[ne(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (1,+inf]]",
	},
	{
		exprStr:     "a > 1",
		accessConds: "[gt(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[(1,+inf]]",
	},
	{
		exprStr:     "1 < a",
		accessConds: "[lt(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[(1,+inf]]",
	},
	{
		exprStr:     "a >= 1",
		accessConds: "[ge(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[1,+inf]]",
	},
	{
		exprStr:     "1 <= a",
		accessConds: "[le(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[1,+inf]]",
	},
	{
		exprStr:     "a < 1",
		accessConds: "[lt(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1)]",
	},
	{
		exprStr:     "1 > a",
		accessConds: "[gt(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1)]",
	},
	{
		exprStr:     "a <= 1",
		accessConds: "[le(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1]]",
	},
	{
		exprStr:     "1 >= test.t.a",
		accessConds: "[ge(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1]]",
	},
	{
		exprStr:     "(a)",
		accessConds: "[test.t.a]",
		filterConds: "[]",
		resultStr:   "[[-inf,0) (0,+inf]]",
	},
	{
		exprStr:     "a in (1, 3, NULL, 2)",
		accessConds: "[in(test.t.a, 1, 3, <nil>, 2)]",
		filterConds: "[]",
		resultStr:   "[[1,1] [2,2] [3,3]]",
	},
	{
		exprStr:     `a IN (8,8,81,45)`,
		accessConds: "[in(test.t.a, 8, 81, 45)]",
		filterConds: "[]",
		resultStr:   `[[8,8] [45,45] [81,81]]`,
	},
	{
		exprStr:     "a between 1 and 2",
		accessConds: "[ge(test.t.a, 1) le(test.t.a, 2)]",
		filterConds: "[]",
		resultStr:   "[[1,2]]",
	},
	{
		exprStr:     "a not between 1 and 2",
		accessConds: "[or(lt(test.t.a, 1), gt(test.t.a, 2))]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (2,+inf]]",
	},
	{
		exprStr:     "a between 2 and 1",
		accessConds: "[ge(test.t.a, 2) le(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		exprStr:     "a not between 2 and 1",
		accessConds: "[or(lt(test.t.a, 2), gt(test.t.a, 1))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		exprStr:     "a IS NULL",
		accessConds: "[isnull(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		exprStr:     "a IS NOT NULL",
		accessConds: "[not(isnull(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		exprStr:     "a IS TRUE",
		accessConds: "[istrue(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,0) (0,+inf]]",
	},
	{
		exprStr:     "a IS NOT TRUE",
		accessConds: "[not(istrue(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[0,0]]",
	},
	{
		exprStr:     "a IS FALSE",
		accessConds: "[isfalse(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[0,0]]",
	},
	{
		exprStr:     "a IS NOT FALSE",
		accessConds: "[not(isfalse(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[-inf,0) (0,+inf]]",
	},
	{
		exprStr:     "a = 1 or a = 3 or a = 4 or (a > 1 and (a = -1 or a = 5))",
		accessConds: "[or(or(eq(test.t.a, 1), eq(test.t.a, 3)), or(eq(test.t.a, 4), and(gt(test.t.a, 1), or(eq(test.t.a, -1), eq(test.t.a, 5)))))]",
		filterConds: "[]",
		resultStr:   "[[1,1] [3,3] [4,4] [5,5]]",
	},
	{
		exprStr:     "(a = 1 and b = 1) or (a = 2 and b = 2)",
		accessConds: "[or(eq(test.t.a, 1), eq(test.t.a, 2))]",
		filterConds: "[or(and(eq(test.t.a, 1), eq(test.t.b, 1)), and(eq(test.t.a, 2), eq(test.t.b, 2)))]",
		resultStr:   "[[1,1] [2,2]]",
	},
	{
		exprStr:     "a = 1 or a = 3 or a = 4 or (b > 1 and (a = -1 or a = 5))",
		accessConds: "[or(or(eq(test.t.a, 1), eq(test.t.a, 3)), or(eq(test.t.a, 4), or(eq(test.t.a, -1), eq(test.t.a, 5))))]",
		filterConds: "[or(or(or(eq(test.t.a, 1), eq(test.t.a, 3)), eq(test.t.a, 4)), and(gt(test.t.b, 1), or(eq(test.t.a, -1), eq(test.t.a, 5))))]",
		resultStr:   "[[-1,-1] [1,1] [3,3] [4,4] [5,5]]",
	},
	{
		exprStr:     "a in (1, 1, 1, 1, 1, 1, 2, 1, 2, 3, 2, 3, 4, 4, 1, 2)",
		accessConds: "[in(test.t.a, 1, 2, 3, 4)]",
		filterConds: "[]",
		resultStr:   "[[1,1] [2,2] [3,3] [4,4]]",
	},
	{
		exprStr:     "a not in (1, 2, 3)",
		accessConds: "[not(in(test.t.a, 1, 2, 3))]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (3,+inf]]",
	},
	{
		exprStr:     "a > 9223372036854775807",
		accessConds: "[gt(test.t.a, 9223372036854775807)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		exprStr:     "a >= 9223372036854775807",
		accessConds: "[ge(test.t.a, 9223372036854775807)]",
		filterConds: "[]",
		resultStr:   "[[9223372036854775807,+inf]]",
	},
	{
		exprStr:     "a < -9223372036854775807",
		accessConds: "[lt(test.t.a, -9223372036854775807)]",
		filterConds: "[]",
		resultStr:   "[[-inf,-9223372036854775807)]",
	},
	{
		exprStr:     "a < -9223372036854775808",
		accessConds: "[lt(test.t.a, -9223372036854775808)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		exprStr:     "isnull(a) or a in (1, 2, 3)",
		accessConds: "[or(isnull(test.t.a), in(test.t.a, 1, 2, 3))]",
		filterConds: "[]",
		resultStr:   "[[1,1] [2,2] [3,3]]",
	},
	{
		exprStr:     "isnull(a) and a in (1, 2, 3)",
		accessConds: "[isnull(test.t.a) in(test.t.a, 1, 2, 3)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
}

// from: pkg/util/ranger/ranger_test.go TestIndexRangeForUnsignedAndOverflow
var rbIndexRangeForUnsignedAndOverflow = []rbCase{
	{
		indexPos:    6,
		exprStr:     `a = 1 and a = 2`,
		accessConds: "[]",
		filterConds: "[]",
		resultStr:   `[]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (0, 1, 2)`,
		accessConds: "[not(in(test.t.a, 0, 1, 2))]",
		filterConds: "[]",
		resultStr:   `[(NULL,0) (2,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (-1, 1, 2)`,
		accessConds: "[not(in(test.t.a, -1, 1, 2))]",
		filterConds: "[]",
		resultStr:   `[(NULL,1) (2,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (-2, -1, 1, 2)`,
		accessConds: "[not(in(test.t.a, -2, -1, 1, 2))]",
		filterConds: "[]",
		resultStr:   `[(NULL,1) (2,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (111)`,
		accessConds: "[ne(test.t.a, 111)]",
		filterConds: "[]",
		resultStr:   `[[-inf,111) (111,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (1, 2, 9223372036854775810)`,
		accessConds: "[not(in(test.t.a, 1, 2, 9223372036854775810))]",
		filterConds: "[]",
		resultStr:   `[(NULL,1) (2,9223372036854775810) (9223372036854775810,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a >= -2147483648`,
		accessConds: "[ge(test.t.a, -2147483648)]",
		filterConds: "[]",
		resultStr:   `[[0,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a > -2147483648`,
		accessConds: "[gt(test.t.a, -2147483648)]",
		filterConds: "[]",
		resultStr:   `[[0,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a != -2147483648`,
		accessConds: "[ne(test.t.a, -2147483648)]",
		filterConds: "[]",
		resultStr:   `[[0,+inf]]`,
	},
	{
		exprStr:     "a < -1 or a < 1",
		accessConds: "[or(lt(test.t.a, -1), lt(test.t.a, 1))]",
		filterConds: "[]",
		resultStr:   "[[-inf,1)]",
	},
	{
		exprStr:     "a < -1 and a < 1",
		accessConds: "[]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    1,
		exprStr:     "decimal_unsigned > -100",
		accessConds: "[gt(test.t.decimal_unsigned, -100)]",
		filterConds: "[]",
		resultStr:   "[[0,+inf]]",
	},
	{
		indexPos:    2,
		exprStr:     "float_unsigned > -100",
		accessConds: "[gt(test.t.float_unsigned, -100)]",
		filterConds: "[]",
		resultStr:   "[[0,+inf]]",
	},
	{
		indexPos:    3,
		exprStr:     "double_unsigned > -100",
		accessConds: "[gt(test.t.double_unsigned, -100)]",
		filterConds: "[]",
		resultStr:   "[[0,+inf]]",
	},
	// test for overflow value access index
	{
		indexPos:    4,
		exprStr:     "col_int != 9223372036854775808",
		accessConds: "[ne(test.t.col_int, 9223372036854775808)]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		indexPos:    4,
		exprStr:     "col_int > 9223372036854775808",
		accessConds: "[gt(test.t.col_int, 9223372036854775808)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    4,
		exprStr:     "col_int < 9223372036854775808",
		accessConds: "[lt(test.t.col_int, 9223372036854775808)]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		indexPos:    5,
		exprStr:     "col_float > 1000000000000000000000000000000000000000",
		accessConds: "[gt(test.t.col_float, 1e+39)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    5,
		exprStr:     "col_float < -1000000000000000000000000000000000000000",
		accessConds: "[lt(test.t.col_float, -1e+39)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
}

// from: pkg/util/ranger/ranger_test.go TestColumnRange
var rbColumnRange = []rbCase{
	{
		colPos:      0,
		exprStr:     "(a = 2 or a = 2) and (a = 2 or a = 2)",
		accessConds: "[or(eq(test.t.a, 2), eq(test.t.a, 2)) or(eq(test.t.a, 2), eq(test.t.a, 2))]",
		filterConds: "[]",
		resultStr:   "[[2,2]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "(a = 2 or a = 1) and (a = 3 or a = 4)",
		accessConds: "[or(eq(test.t.a, 2), eq(test.t.a, 1)) or(eq(test.t.a, 3), eq(test.t.a, 4))]",
		filterConds: "[]",
		resultStr:   "[]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a = 1 and b > 1",
		accessConds: "[eq(test.t.a, 1)]",
		filterConds: "[gt(test.t.b, 1)]",
		resultStr:   "[[1,1]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      1,
		exprStr:     "b > 1",
		accessConds: "[gt(test.t.b, 1)]",
		filterConds: "[]",
		resultStr:   "[(1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "1 = a",
		accessConds: "[eq(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[1,1]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a != 1",
		accessConds: "[ne(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "1 != a",
		accessConds: "[ne(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a > 1",
		accessConds: "[gt(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[(1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "1 < a",
		accessConds: "[lt(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[(1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a >= 1",
		accessConds: "[ge(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "1 <= a",
		accessConds: "[le(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[1,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a < 1",
		accessConds: "[lt(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1)]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "1 > a",
		accessConds: "[gt(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1)]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a <= 1",
		accessConds: "[le(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "1 >= a",
		accessConds: "[ge(1, test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,1]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "(a)",
		accessConds: "[test.t.a]",
		filterConds: "[]",
		resultStr:   "[[-inf,0) (0,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a in (1, 3, NULL, 2)",
		accessConds: "[in(test.t.a, 1, 3, <nil>, 2)]",
		filterConds: "[]",
		resultStr:   "[[1,1] [2,2] [3,3]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     `a IN (8,8,81,45)`,
		accessConds: "[in(test.t.a, 8, 81, 45)]",
		filterConds: "[]",
		resultStr:   `[[8,8] [45,45] [81,81]]`,
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a between 1 and 2",
		accessConds: "[ge(test.t.a, 1) le(test.t.a, 2)]",
		filterConds: "[]",
		resultStr:   "[[1,2]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a not between 1 and 2",
		accessConds: "[or(lt(test.t.a, 1), gt(test.t.a, 2))]",
		filterConds: "[]",
		resultStr:   "[[-inf,1) (2,+inf]]",
		length:      types.UnspecifiedLength,
	},
	// {
	//  `a > null` will be converted to `castAsString(a) > null` which can not be extracted as access condition.
	// 	exprStr:   "a not between null and 0",
	// 	resultStr[(0,+inf]]
	// },
	{
		colPos:      0,
		exprStr:     "a between 2 and 1",
		accessConds: "[ge(test.t.a, 2) le(test.t.a, 1)]",
		filterConds: "[]",
		resultStr:   "[]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a not between 2 and 1",
		accessConds: "[or(lt(test.t.a, 2), gt(test.t.a, 1))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a IS NULL",
		accessConds: "[isnull(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[NULL,NULL]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a IS NOT NULL",
		accessConds: "[not(isnull(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a IS TRUE",
		accessConds: "[istrue(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[-inf,0) (0,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a IS NOT TRUE",
		accessConds: "[not(istrue(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[NULL,NULL] [0,0]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a IS FALSE",
		accessConds: "[isfalse(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[0,0]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a IS NOT FALSE",
		accessConds: "[not(isfalse(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[NULL,0) (0,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      1,
		exprStr:     `b in (1, '2.1')`,
		accessConds: "[in(test.t.b, 1, 2.1)]",
		filterConds: "[]",
		resultStr:   "[[1,1] [2.1,2.1]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     `a > 9223372036854775807`,
		accessConds: "[gt(test.t.a, 9223372036854775807)]",
		filterConds: "[]",
		resultStr:   "[(9223372036854775807,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      2,
		exprStr:     `c > 111.11111111`,
		accessConds: "[gt(test.t.c, 111.11111111)]",
		filterConds: "[]",
		resultStr:   "[[111.111115,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      3,
		exprStr:     `d > 'aaaaaaaaaaaaaa'`,
		accessConds: "[gt(test.t.d, aaaaaaaaaaaaaa)]",
		filterConds: "[]",
		resultStr:   "[(\"aaaaaaaaaaaaaa\",+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      4,
		exprStr:     `e > 18446744073709500000`,
		accessConds: "[gt(test.t.e, 18446744073709500000)]",
		filterConds: "[]",
		resultStr:   "[(18446744073709500000,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      4,
		exprStr:     `e > -2147483648`,
		accessConds: "[gt(test.t.e, -2147483648)]",
		filterConds: "[]",
		resultStr:   "[[0,+inf]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      3,
		exprStr:     "d = 'aab' or d = 'aac'",
		accessConds: "[or(eq(test.t.d, aab), eq(test.t.d, aac))]",
		filterConds: "[]",
		resultStr:   "[[\"a\",\"a\"]]",
		length:      1,
	},
	// This test case cannot be simplified to [1, 3] otherwise the index join will executes wrongly.
	{
		colPos:      0,
		exprStr:     "a in (1, 2, 3)",
		accessConds: "[in(test.t.a, 1, 2, 3)]",
		filterConds: "",
		resultStr:   "[[1,1] [2,2] [3,3]]",
		length:      types.UnspecifiedLength,
	},
	// test cases for nulleq
	{
		colPos:      0,
		exprStr:     "a <=> 1",
		accessConds: "[nulleq(test.t.a, 1)]",
		filterConds: "",
		resultStr:   "[[1,1]]",
		length:      types.UnspecifiedLength,
	},
	{
		colPos:      0,
		exprStr:     "a <=> null",
		accessConds: "[nulleq(test.t.a, <nil>)]",
		filterConds: "",
		resultStr:   "[[NULL,NULL]]",
		length:      types.UnspecifiedLength,
	},
}

// from: pkg/util/ranger/ranger_test.go TestIndexRangeForYear
var rbIndexRangeForYear = []rbCase{
	{
		indexPos:    0,
		exprStr:     `a not in (0, 1, 2)`,
		accessConds: "[not(in(test.t.a, 0, 1, 2))]",
		filterConds: "[]",
		resultStr:   `[(NULL,0) (0,2001) (2002,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (-1, 1, 2)`,
		accessConds: "[not(in(test.t.a, -1, 1, 2))]",
		filterConds: "[]",
		resultStr:   `[(NULL,2001) (2002,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (1, 2, 70)`,
		accessConds: "[not(in(test.t.a, 1, 2, 70))]", // this is in accordance with MySQL, MySQL won't interpret 70 here as 1970
		filterConds: "[]",
		resultStr:   `[(NULL,1970) (1970,2001) (2002,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a = 1 or a = 2 or a = 70`,
		accessConds: "[or(eq(test.t.a, 2001), or(eq(test.t.a, 2002), eq(test.t.a, 1970)))]", // this is in accordance with MySQL, MySQL won't interpret 70 here as 1970
		filterConds: "[]",
		resultStr:   `[[1970,1970] [2001,2002]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (99)`,
		accessConds: "[ne(test.t.a, 1999)]", // this is in accordance with MySQL
		filterConds: "[]",
		resultStr:   `[[-inf,1999) (1999,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a not in (1, 2, 15698)`,
		accessConds: "[not(in(test.t.a, 1, 2, 15698))]",
		filterConds: "[]",
		resultStr:   `[(NULL,2001) (2002,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a >= -1000`,
		accessConds: "[ge(test.t.a, -1000)]",
		filterConds: "[]",
		resultStr:   `[[0,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a > -1000`,
		accessConds: "[gt(test.t.a, -1000)]",
		filterConds: "[]",
		resultStr:   `[[0,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a != 1`,
		accessConds: "[ne(test.t.a, 2001)]",
		filterConds: "[]",
		resultStr:   `[[-inf,2001) (2001,+inf]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a != 2156`,
		accessConds: "[ne(test.t.a, 2156)]",
		filterConds: "[]",
		resultStr:   `[[-inf,+inf]]`,
	},
	{
		exprStr:     "a < 99 or a > 01",
		accessConds: "[or(lt(test.t.a, 1999), gt(test.t.a, 2001))]",
		filterConds: "[]",
		resultStr:   "[[-inf,1999) (2001,+inf]]",
	},
	{
		exprStr:     "a >= 70 and a <= 69",
		accessConds: "[ge(test.t.a, 1970) le(test.t.a, 2069)]",
		filterConds: "[]",
		resultStr:   "[[1970,2069]]",
	},
}

// from: pkg/util/ranger/ranger_test.go TestPrefixIndexRangeScan
var rbPrefixIndexRangeScan = []rbCase{
	{
		indexPos:    0,
		exprStr:     "a > 'aa'",
		accessConds: "[gt(test.t.a, aa)]",
		filterConds: "[gt(test.t.a, aa)]",
		resultStr:   "[[\"aa\",+inf]]",
	},
	{
		indexPos:    1,
		exprStr:     "a = 'aaa' and b > 'bb' and b < 'cc'",
		accessConds: "[eq(test.t.a, aaa) gt(test.t.b, bb) lt(test.t.b, cc)]",
		filterConds: "[eq(test.t.a, aaa) gt(test.t.b, bb) lt(test.t.b, cc)]",
		resultStr:   "[[\"aa\" \"bb\",\"aa\" \"cc\")]",
	},
}

// from: pkg/util/ranger/ranger_test.go TestIndexRange
var rbIndexRange = []rbCase{
	{
		indexPos:    0,
		exprStr:     `a LIKE 'abc%'`,
		accessConds: `[like(test.t.a, abc%, 92)]`,
		filterConds: "[like(test.t.a, abc%, 92)]",
		resultStr:   "[[\"abc\",\"abd\")]",
	},
	{
		indexPos:    0,
		exprStr:     "a LIKE 'abc_'",
		accessConds: "[like(test.t.a, abc_, 92)]",
		filterConds: "[like(test.t.a, abc_, 92)]",
		resultStr:   "[[\"abc\",\"abd\")]",
	},
	{
		indexPos:    0,
		exprStr:     "a LIKE 'abc'",
		accessConds: "[like(test.t.a, abc, 92)]",
		filterConds: "[like(test.t.a, abc, 92)]",
		resultStr:   "[[\"abc\",\"abc\"]]",
	},
	{
		indexPos:    0,
		exprStr:     `a LIKE "ab\_c"`,
		accessConds: "[like(test.t.a, ab\\_c, 92)]",
		filterConds: "[like(test.t.a, ab\\_c, 92)]",
		resultStr:   "[[\"ab_c\",\"ab_c\"]]",
	},
	{
		indexPos:    0,
		exprStr:     `a LIKE '%'`,
		accessConds: "[]",
		filterConds: `[like(test.t.a, %, 92)]`,
		resultStr:   "[[NULL,+inf]]",
	},
	{
		indexPos:    0,
		exprStr:     `a LIKE '\%a'`,
		accessConds: "[like(test.t.a, \\%a, 92)]",
		filterConds: "[like(test.t.a, \\%a, 92)]",
		resultStr:   `[["%a","%a"]]`,
	},
	{
		indexPos:    0,
		exprStr:     `a LIKE "\\"`,
		accessConds: "[like(test.t.a, \\, 92)]",
		filterConds: "[like(test.t.a, \\, 92)]",
		resultStr:   "[[\"\\\\\",\"\\\\\"]]",
	},
	{
		indexPos:    0,
		exprStr:     `a LIKE "\\\\a%"`,
		accessConds: `[like(test.t.a, \\a%, 92)]`,
		filterConds: "[like(test.t.a, \\\\a%, 92)]",
		resultStr:   "[[\"\\\\a\",\"\\\\b\")]",
	},
	{
		indexPos:    0,
		exprStr:     `a > NULL`,
		accessConds: "[gt(test.t.a, <nil>)]",
		filterConds: "[]",
		resultStr:   `[]`,
	},
	{
		indexPos:    0,
		exprStr:     `a = 'a' and b in (1, 2, 3)`,
		accessConds: "[eq(test.t.a, a) in(test.t.b, 1, 2, 3)]",
		filterConds: "[]",
		resultStr:   "[[\"a\" 1,\"a\" 1] [\"a\" 2,\"a\" 2] [\"a\" 3,\"a\" 3]]",
	},
	{
		indexPos:    0,
		exprStr:     `a = 'a' and b not in (1, 2, 3)`,
		accessConds: "[eq(test.t.a, a) not(in(test.t.b, 1, 2, 3))]",
		filterConds: "[]",
		resultStr:   "[(\"a\" NULL,\"a\" 1) (\"a\" 3,\"a\" +inf]]",
	},
	{
		indexPos:    0,
		exprStr:     `a in ('a') and b in ('1', 2.0, NULL)`,
		accessConds: "[eq(test.t.a, a) in(test.t.b, 1, 2, <nil>)]",
		filterConds: "[]",
		resultStr:   `[["a" 1,"a" 1] ["a" 2,"a" 2]]`,
	},
	{
		indexPos:    1,
		exprStr:     `c in ('1.1', 1, 1.1) and a in ('1', 'a', NULL)`,
		accessConds: "[in(test.t.c, 1.1, 1) in(test.t.a, 1, a, <nil>)]",
		filterConds: "[]",
		resultStr:   "[[1 \"1\",1 \"1\"] [1 \"a\",1 \"a\"] [1.1 \"1\",1.1 \"1\"] [1.1 \"a\",1.1 \"a\"]]",
	},
	{
		indexPos:    1,
		exprStr:     "c in (1, 1, 1, 1, 1, 1, 2, 1, 2, 3, 2, 3, 4, 4, 1, 2)",
		accessConds: "[in(test.t.c, 1, 2, 3, 4)]",
		filterConds: "[]",
		resultStr:   "[[1,1] [2,2] [3,3] [4,4]]",
	},
	{
		indexPos:    1,
		exprStr:     "c not in (1, 2, 3)",
		accessConds: "[not(in(test.t.c, 1, 2, 3))]",
		filterConds: "[]",
		resultStr:   "[(NULL,1) (1,2) (2,3) (3,+inf]]",
	},
	{
		indexPos:    1,
		exprStr:     "c in (1, 2) and c in (1, 3)",
		accessConds: "[eq(test.t.c, 1)]",
		filterConds: "[]",
		resultStr:   "[[1,1]]",
	},
	{
		indexPos:    1,
		exprStr:     "c = 1 and c = 2",
		accessConds: "[]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    0,
		exprStr:     "a in (NULL)",
		accessConds: "[eq(test.t.a, <nil>)]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    0,
		exprStr:     "a not in (NULL, '1', '2', '3')",
		accessConds: "[not(in(test.t.a, <nil>, 1, 2, 3))]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    0,
		exprStr:     "not (a not in (NULL, '1', '2', '3') and a > '2')",
		accessConds: "[or(in(test.t.a, <nil>, 1, 2, 3), le(test.t.a, 2))]",
		filterConds: "[]",
		resultStr:   "[[-inf,\"2\"] [\"3\",\"3\"]]",
	},
	{
		indexPos:    0,
		exprStr:     "not (a not in (NULL) and a > '2')",
		accessConds: "[or(eq(test.t.a, <nil>), le(test.t.a, 2))]",
		filterConds: "[]",
		resultStr:   "[[-inf,\"2\"]]",
	},
	{
		indexPos:    0,
		exprStr:     "not (a not in (NULL) or a > '2')",
		accessConds: "[and(eq(test.t.a, <nil>), le(test.t.a, 2))]",
		filterConds: "[]",
		resultStr:   "[]",
	},
	{
		indexPos:    0,
		exprStr:     "(a > 'b' and a < 'bbb') or (a < 'cb' and a > 'a')",
		accessConds: "[or(and(gt(test.t.a, b), lt(test.t.a, bbb)), and(lt(test.t.a, cb), gt(test.t.a, a)))]",
		filterConds: "[]",
		resultStr:   "[(\"a\",\"cb\")]",
	},
	{
		indexPos:    0,
		exprStr:     "(a > 'a' and a < 'b') or (a >= 'b' and a < 'c')",
		accessConds: "[or(and(gt(test.t.a, a), lt(test.t.a, b)), and(ge(test.t.a, b), lt(test.t.a, c)))]",
		filterConds: "[]",
		resultStr:   "[(\"a\",\"c\")]",
	},
	{
		indexPos:    0,
		exprStr:     "(a > 'a' and a < 'b' and b < 1) or (a >= 'b' and a < 'c')",
		accessConds: "[or(and(gt(test.t.a, a), lt(test.t.a, b)), and(ge(test.t.a, b), lt(test.t.a, c)))]",
		filterConds: "[or(and(and(gt(test.t.a, a), lt(test.t.a, b)), lt(test.t.b, 1)), and(ge(test.t.a, b), lt(test.t.a, c)))]",
		resultStr:   "[(\"a\",\"c\")]",
	},
	{
		indexPos:    0,
		exprStr:     "(a in ('a', 'b') and b < 1) or (a >= 'b' and a < 'c')",
		accessConds: "[or(and(in(test.t.a, a, b), lt(test.t.b, 1)), and(ge(test.t.a, b), lt(test.t.a, c)))]",
		filterConds: "[]",
		resultStr:   `[["a" -inf,"a" 1) ["b","c")]`,
	},
	{
		indexPos:    0,
		exprStr:     "(a > 'a') or (c > 1)",
		accessConds: "[]",
		filterConds: "[or(gt(test.t.a, a), gt(test.t.c, 1))]",
		resultStr:   "[[NULL,+inf]]",
	},
	{
		indexPos:    2,
		exprStr:     `d = "你好啊"`,
		accessConds: "[eq(test.t.d, 你好啊)]",
		filterConds: "[eq(test.t.d, 你好啊)]",
		resultStr:   "[[\"你好\",\"你好\"]]",
	},
	{
		indexPos:    3,
		exprStr:     `e = "你好啊"`,
		accessConds: "[eq(test.t.e, 你好啊)]",
		filterConds: "[eq(test.t.e, 你好啊)]",
		resultStr:   "[[\"\\xe4\\xbd\",\"\\xe4\\xbd\"]]",
	},
	{
		indexPos:    2,
		exprStr:     `d in ("你好啊", "再见")`,
		accessConds: "[in(test.t.d, 你好啊, 再见)]",
		filterConds: "[in(test.t.d, 你好啊, 再见)]",
		resultStr:   "[[\"你好\",\"你好\"] [\"再见\",\"再见\"]]",
	},
	{
		indexPos:    2,
		exprStr:     `d not in ("你好啊")`,
		accessConds: "[]",
		filterConds: "[ne(test.t.d, 你好啊)]",
		resultStr:   "[[NULL,+inf]]",
	},
	{
		indexPos:    2,
		exprStr:     `d < "你好" || d > "你好"`,
		accessConds: "[or(lt(test.t.d, 你好), gt(test.t.d, 你好))]",
		filterConds: "[or(lt(test.t.d, 你好), gt(test.t.d, 你好))]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		indexPos:    2,
		exprStr:     `not(d < "你好" || d > "你好")`,
		accessConds: "[and(ge(test.t.d, 你好), le(test.t.d, 你好))]",
		filterConds: "[and(ge(test.t.d, 你好), le(test.t.d, 你好))]",
		resultStr:   "[[\"你好\",\"你好\"]]",
	},
	{
		indexPos:    4,
		exprStr:     "f >= 'a' and f <= 'B'",
		accessConds: "[ge(test.t.f, a) le(test.t.f, B)]",
		filterConds: "[]",
		resultStr:   "[[\"\\x00A\",\"\\x00B\"]]",
	},
	{
		indexPos:    4,
		exprStr:     "f in ('a', 'B')",
		accessConds: "[in(test.t.f, a, B)]",
		filterConds: "[]",
		resultStr:   "[[\"\\x00A\",\"\\x00A\"] [\"\\x00B\",\"\\x00B\"]]",
	},
	{
		indexPos:    4,
		exprStr:     "f = 'a' and f = 'B' collate utf8mb4_bin",
		accessConds: "[eq(test.t.f, a)]",
		filterConds: "[eq(test.t.f, B)]",
		resultStr:   "[[\"\\x00A\",\"\\x00A\"]]",
	},
	{
		indexPos:    4,
		exprStr:     "f like '@%' collate utf8mb4_bin",
		accessConds: "[]",
		filterConds: "[like(test.t.f, @%, 92)]",
		resultStr:   "[[NULL,+inf]]",
	},
	{
		indexPos:    5,
		exprStr:     "d in ('aab', 'aac') and e = 'a'",
		accessConds: "[in(test.t.d, aab, aac) eq(test.t.e, a)]",
		filterConds: "[in(test.t.d, aab, aac)]",
		resultStr:   "[[\"aa\" \"a\",\"aa\" \"a\"]]",
	},
	{
		indexPos:    6,
		exprStr:     "g = 'a'",
		accessConds: "[eq(test.t.g, a)]",
		filterConds: "[]",
		resultStr:   "[[\"A\",\"A\"]]",
	},
	{
		indexPos:    7,
		exprStr:     `h LIKE 'ÿÿ%'`,
		accessConds: `[like(test.t.h, ÿÿ%, 92)]`,
		filterConds: "[like(test.t.h, ÿÿ%, 92)]",
		resultStr:   "[[\"ÿÿ\",\"ÿ\\xc3\\xc0\")]", // The decoding error is ignored.
	},
	// CAST AS BINARY on CI column: EQ should use range scan with filter.
	{
		indexPos:    4,
		exprStr:     "f = cast('a' as binary)",
		accessConds: "[eq(test.t.f, a)]",
		filterConds: "[eq(test.t.f, a)]",
		resultStr:   "[[\"\\x00A\",\"\\x00A\"]]",
	},
	// CAST AS BINARY on CI column: IN should use range scan with filter.
	{
		indexPos:    4,
		exprStr:     "f in (cast('a' as binary), cast('B' as binary))",
		accessConds: "[in(test.t.f, a, B)]",
		filterConds: "[in(test.t.f, a, B)]",
		resultStr:   "[[\"\\x00A\",\"\\x00A\"] [\"\\x00B\",\"\\x00B\"]]",
	},
}

// from: pkg/util/ranger/ranger_test.go TestPrefixIndexRange
var rbPrefixIndexRange = []rbCase{
	{
		indexPos:    0,
		exprStr:     "a is null",
		accessConds: "[isnull(test.t.a)]",
		filterConds: "[]",
		resultStr:   "[[NULL,NULL]]",
	},
	{
		indexPos:    0,
		exprStr:     "isnull(a) or a in (1,2,3,4)",
		accessConds: "[]",
		filterConds: "[or(isnull(test.t.a), or(or(eq(cast(test.t.a, double BINARY), 1), eq(cast(test.t.a, double BINARY), 2)), or(eq(cast(test.t.a, double BINARY), 3), eq(cast(test.t.a, double BINARY), 4))))]",
		resultStr:   "[[NULL,+inf]]",
	},
	{
		indexPos:    0,
		exprStr:     "isnull(a) and a in (1,2,3,4)",
		accessConds: "[isnull(test.t.a)]",
		filterConds: "[or(or(eq(cast(test.t.a, double BINARY), 1), eq(cast(test.t.a, double BINARY), 2)), or(eq(cast(test.t.a, double BINARY), 3), eq(cast(test.t.a, double BINARY), 4)))]",
		resultStr:   "[[NULL,NULL]]",
	},
	{
		indexPos:    0,
		exprStr:     "a is not null",
		accessConds: "[not(isnull(test.t.a))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		indexPos:    1,
		exprStr:     "a = 'a' and b is null",
		accessConds: "[eq(test.t.a, a) isnull(test.t.b)]",
		filterConds: "[eq(test.t.a, a)]",
		resultStr:   "[[\"a\" NULL,\"a\" NULL]]",
	},
	{
		indexPos:    1,
		exprStr:     "a = 'a' and b is not null",
		accessConds: "[eq(test.t.a, a) not(isnull(test.t.b))]",
		filterConds: "[eq(test.t.a, a)]",
		resultStr:   "[[\"a\" -inf,\"a\" +inf]]",
	},
	{
		indexPos:    2,
		exprStr:     "c is null",
		accessConds: "[isnull(test.t.c)]",
		filterConds: "[]",
		resultStr:   "[[NULL,NULL]]",
	},
	{
		indexPos:    2,
		exprStr:     "c is not null",
		accessConds: "[not(isnull(test.t.c))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
	{
		indexPos:    3,
		exprStr:     "d is null",
		accessConds: "[isnull(test.t.d)]",
		filterConds: "[]",
		resultStr:   "[[NULL,NULL]]",
	},
	{
		indexPos:    3,
		exprStr:     "d is not null",
		accessConds: "[not(isnull(test.t.d))]",
		filterConds: "[]",
		resultStr:   "[[-inf,+inf]]",
	},
}

// from: pkg/util/ranger/ranger_test.go TestMinAccessCondsForDNFCond
var rbMinAccessCondsForDNFCond = []rbCase{
	{
		indexPos:                 0,
		exprStr:                  "a = 1",
		accessConds:              "[eq(test.t.a, 1)]",
		minAccessCondsForDNFCond: 0,
	},
	{
		indexPos:                 0,
		exprStr:                  "a = 1 or a = 2 or a = 3",
		accessConds:              "[or(eq(test.t.a, 1), or(eq(test.t.a, 2), eq(test.t.a, 3)))]",
		minAccessCondsForDNFCond: 1,
	},
	{
		indexPos:                 0,
		exprStr:                  "a = 1 or b = 2 or c = 3",
		accessConds:              "[]",
		minAccessCondsForDNFCond: 0,
	},
	{
		indexPos:                 0,
		exprStr:                  "(a=1 and b=2) or (a=3 and b=4) or (a=5 and b=6 and c=7)",
		accessConds:              "[or(eq(test.t.a, 1), or(eq(test.t.a, 3), eq(test.t.a, 5)))]",
		minAccessCondsForDNFCond: 1,
	},
	{
		indexPos:                 1,
		exprStr:                  "(a=1 and b=2) or (a=3 and b=4) or (a=5 and b=6 and c=7)",
		accessConds:              "[or(eq(test.t.b, 2), or(eq(test.t.b, 4), eq(test.t.b, 6)))]",
		minAccessCondsForDNFCond: 1,
	},
	{
		indexPos:                 2,
		exprStr:                  "(a=1 and b=2) or (a=3 and b=4) or (a=5 and b=6 and c=7)",
		accessConds:              "[]",
		minAccessCondsForDNFCond: 0,
	},
	{
		indexPos:                 3,
		exprStr:                  "(a=1 and b=2) or (a=3 and b=4) or (a=5 and b=6 and c=7)",
		accessConds:              "[or(and(eq(test.t.a, 1), eq(test.t.b, 2)), or(and(eq(test.t.a, 3), eq(test.t.b, 4)), and(eq(test.t.a, 5), and(eq(test.t.b, 6), eq(test.t.c, 7)))))]",
		minAccessCondsForDNFCond: 2,
	},
	{
		indexPos:                 4,
		exprStr:                  "(a=1 and b=2) or (a=3 and b=4) or (a=5 and b=6 and c=7)",
		accessConds:              "[or(and(eq(test.t.a, 1), eq(test.t.b, 2)), or(and(eq(test.t.a, 3), eq(test.t.b, 4)), and(eq(test.t.a, 5), eq(test.t.b, 6))))]",
		minAccessCondsForDNFCond: 2,
	},
	{
		indexPos:                 3,
		exprStr:                  "(a=1) or (a=3 and b=4) or (a=5 and b=6 and c=7)",
		accessConds:              "[or(eq(test.t.a, 1), or(and(eq(test.t.a, 3), eq(test.t.b, 4)), and(eq(test.t.a, 5), and(eq(test.t.b, 6), eq(test.t.c, 7)))))]",
		minAccessCondsForDNFCond: 1,
	},
}

// from: pkg/util/ranger/ranger_test.go TestTableShardIndex
var rbTableShardIndex = []rbCase{
	{
		exprStr:     "a = 1",
		accessConds: "[eq(tidb_shard(test.test3.a), 214) eq(test.test3.a, 1)]",
		tableName:   "test3",
	},
	{
		exprStr: "a=100 and (b = 100 or b = 200)",
		accessConds: "[or(eq(test.test3.b, 100), eq(test.test3.b, 200)) eq(tidb_shard(test.test3.a), 8) " +
			"eq(test.test3.a, 100)]",
		tableName: "test3",
	},
	{
		// don't add prefix
		exprStr:     " tidb_shard(a) = 8",
		accessConds: "[eq(tidb_shard(test.test3.a), 8)]",
		tableName:   "test3",
	},
	{
		exprStr: "a=100 or b = 200",
		accessConds: "[or(and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100)), " +
			"eq(test.test3.b, 200))]",
		tableName: "test3",
	},
	{
		exprStr: "a=100 or b > 200",
		accessConds: "[or(and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100)), " +
			"gt(test.test3.b, 200))]",
		tableName: "test3",
	},
	{
		exprStr: "a=100 or a = 200  or 1",
		accessConds: "[or(and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100)), " +
			"or(and(eq(tidb_shard(test.test3.a), 161), eq(test.test3.a, 200)), 1))]",
		tableName: "test3",
	},
	{
		exprStr: "(a=100 and b = 100) or a = 300",
		accessConds: "[or(and(eq(test.test3.b, 100), and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100))), " +
			"and(eq(tidb_shard(test.test3.a), 227), eq(test.test3.a, 300)))]",
		tableName: "test3",
	},
	{
		exprStr: "((a=100 and b = 100) or a = 200) or a = 300",
		accessConds: "[or(and(eq(test.test3.b, 100), and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100))), " +
			"or(and(eq(tidb_shard(test.test3.a), 161), eq(test.test3.a, 200)), " +
			"and(eq(tidb_shard(test.test3.a), 227), eq(test.test3.a, 300))))]",
		tableName: "test3",
	},
	{
		exprStr: "a IN (100, 200, 300)",
		accessConds: "[or(or(and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100)), " +
			"and(eq(tidb_shard(test.test3.a), 161), eq(test.test3.a, 200))), and(eq(tidb_shard(test.test3.a), 227), eq(test.test3.a, 300)))]",
		tableName: "test3",
	},
	{
		exprStr:     "a IN (100)",
		accessConds: "[eq(tidb_shard(test.test3.a), 8) eq(test.test3.a, 100)]",
		tableName:   "test3",
	},
	{
		exprStr: "a IN (100, 200, 300) or a = 400",
		accessConds: "[or(or(or(and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100)), " +
			"and(eq(tidb_shard(test.test3.a), 161), eq(test.test3.a, 200))), and(eq(tidb_shard(test.test3.a), 227), eq(test.test3.a, 300))), and(eq(tidb_shard(test.test3.a), 85), eq(test.test3.a, 400)))]",
		tableName: "test3",
	},
	{
		// don't add prefix
		exprStr: "((a=100 and b = 100) or a = 200) and b = 300",
		accessConds: "[or(and(eq(test.test3.a, 100), eq(test.test3.b, 100)), eq(test.test3.a, 200)) " +
			"eq(test.test3.b, 300)]",
		tableName: "test3",
	},
	{
		// don't add prefix
		exprStr:     "a = b",
		accessConds: "[eq(test.test3.a, test.test3.b)]",
		tableName:   "test3",
	},
	{
		// don't add prefix
		exprStr:     "a = b and b = 100",
		accessConds: "[eq(test.test3.a, test.test3.b) eq(test.test3.b, 100)]",
		tableName:   "test3",
	},
	{
		// don't add prefix
		exprStr:     "a > 900",
		accessConds: "[gt(test.test3.a, 900)]",
		tableName:   "test3",
	},
	{
		// add prefix
		exprStr:     "a = 3 or a > 900",
		accessConds: "[or(and(eq(tidb_shard(test.test3.a), 156), eq(test.test3.a, 3)), gt(test.test3.a, 900))]",
		tableName:   "test3",
	},
	// two shard index in one table
	{
		exprStr:     "a = 100",
		accessConds: "[eq(tidb_shard(test.test4.a), 8) eq(test.test4.a, 100)]",
		tableName:   "test4",
	},
	{
		exprStr:     "b = 100",
		accessConds: "[eq(tidb_shard(test.test4.b), 8) eq(test.test4.b, 100)]",
		tableName:   "test4",
	},
	{
		exprStr: "a = 100 and b = 100",
		accessConds: "[eq(tidb_shard(test.test4.a), 8) eq(test.test4.a, 100) " +
			"eq(tidb_shard(test.test4.b), 8) eq(test.test4.b, 100)]",
		tableName: "test4",
	},
	{
		exprStr: "a = 100 or b = 100",
		accessConds: "[or(and(eq(tidb_shard(test.test4.a), 8), eq(test.test4.a, 100)), " +
			"and(eq(tidb_shard(test.test4.b), 8), eq(test.test4.b, 100)))]",
		tableName: "test4",
	},
	// shard index cotans three fields
	{
		exprStr:     "a = 100 and b = 100",
		accessConds: "[eq(tidb_shard(test.test5.a), 8) eq(test.test5.a, 100) eq(test.test5.b, 100)]",
		tableName:   "test5",
	},
	{
		exprStr: "(a=100 and b = 100) or  (a=200 and b = 200)",
		accessConds: "[or(and(eq(tidb_shard(test.test5.a), 8), and(eq(test.test5.a, 100), eq(test.test5.b, 100))), " +
			"and(eq(tidb_shard(test.test5.a), 161), and(eq(test.test5.a, 200), eq(test.test5.b, 200))))]",
		tableName: "test5",
	},
	{
		exprStr: "(a, b) in ((100, 100), (200, 200))",
		accessConds: "[or(and(eq(tidb_shard(test.test5.a), 8), and(eq(test.test5.a, 100), eq(test.test5.b, 100))), " +
			"and(eq(tidb_shard(test.test5.a), 161), and(eq(test.test5.a, 200), eq(test.test5.b, 200))))]",
		tableName: "test5",
	},
	{
		exprStr:     "(a, b) in ((100, 100))",
		accessConds: "[eq(tidb_shard(test.test5.a), 8) eq(test.test5.a, 100) eq(test.test5.b, 100)]",
		tableName:   "test5",
	},
	// don't add prefix
	{
		exprStr:     "a=100",
		accessConds: "[eq(test.testy.a, 100)]",
		tableName:   "testy",
	},
	// don't add prefix
	{
		exprStr:     "a=100",
		accessConds: "[eq(test.testz.a, 100)]",
		tableName:   "testz",
	},
	// test join
	{
		exprStr:     "test3.a = 100",
		accessConds: "[eq(tidb_shard(test.test3.a), 8) eq(test.test3.a, 100)]",
		childLevel:  4,
		tableName:   "test3 JOIN test33 ON test3.b = test33.b",
	},
	{
		exprStr:     "test3.a = 100 and test33.a > 10",
		accessConds: "[gt(test.test33.a, 10) eq(tidb_shard(test.test3.a), 8) eq(test.test3.a, 100)]",
		childLevel:  4,
		tableName:   "test3 JOIN test33 ON test3.b = test33.b",
	},
	{
		exprStr:     "test3.a = 100 AND test6.a = 10",
		accessConds: "[eq(test.test6.a, 10) eq(tidb_shard(test.test3.a), 8) eq(test.test3.a, 100)]",
		childLevel:  4,
		tableName:   "test3 JOIN test6 ON test3.b = test6.b",
	},
	{
		exprStr:     "test3.a = 100 or test6.a = 10",
		accessConds: "[or(and(eq(tidb_shard(test.test3.a), 8), eq(test.test3.a, 100)), eq(test.test6.a, 10))]",
		childLevel:  4,
		tableName:   "test3 JOIN test6 ON test3.b = test6.b",
	},
}
