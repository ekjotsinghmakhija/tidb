// Copyright 2025 Ekjot Singh
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package executor_test

import (
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/util/testkit"
)

func (s *testSuite4) TestDirtyTransaction(c *C) {
	tk := testkit.NewTestKit(c, s.store)
	tk.MustExec("use test")
	tk.MustExec("drop table if exists t")
	tk.MustExec("create table t (a int primary key, b int, index idx_b (b));")
	tk.MustExec("insert t value (2, 3), (4, 8), (6, 8)")
	tk.MustExec("begin")
	tk.MustQuery("select * from t").Check(testkit.Rows("2 3", "4 8", "6 8"))
	tk.MustExec("insert t values (1, 5), (3, 4), (7, 6)")
	tk.MustQuery("select * from information_schema.columns")
	tk.MustQuery("select * from t").Check(testkit.Rows("1 5", "2 3", "3 4", "4 8", "6 8", "7 6"))
	tk.MustQuery("select * from t where a = 1").Check(testkit.Rows("1 5"))
	tk.MustQuery("select * from t order by a desc").Check(testkit.Rows("7 6", "6 8", "4 8", "3 4", "2 3", "1 5"))
	tk.MustQuery("select * from t order by b, a").Check(testkit.Rows("2 3", "3 4", "1 5", "7 6", "4 8", "6 8"))
	tk.MustQuery("select * from t order by b desc, a desc").Check(testkit.Rows("6 8", "4 8", "7 6", "1 5", "3 4", "2 3"))
	tk.MustQuery("select b from t where b = 8 order by b desc").Check(testkit.Rows("8", "8"))
	// Delete a snapshot row and a dirty row.
	tk.MustExec("delete from t where a = 2 or a = 3")
	tk.MustQuery("select * from t").Check(testkit.Rows("1 5", "4 8", "6 8", "7 6"))
	tk.MustQuery("select * from t order by a desc").Check(testkit.Rows("7 6", "6 8", "4 8", "1 5"))
	tk.MustQuery("select * from t order by b, a").Check(testkit.Rows("1 5", "7 6", "4 8", "6 8"))
	tk.MustQuery("select * from t order by b desc, a desc").Check(testkit.Rows("6 8", "4 8", "7 6", "1 5"))
	// Add deleted row back.
	tk.MustExec("insert t values (2, 3), (3, 4)")
	tk.MustQuery("select * from t").Check(testkit.Rows("1 5", "2 3", "3 4", "4 8", "6 8", "7 6"))
	tk.MustQuery("select * from t order by a desc").Check(testkit.Rows("7 6", "6 8", "4 8", "3 4", "2 3", "1 5"))
	tk.MustQuery("select * from t order by b, a").Check(testkit.Rows("2 3", "3 4", "1 5", "7 6", "4 8", "6 8"))
	tk.MustQuery("select * from t order by b desc, a desc").Check(testkit.Rows("6 8", "4 8", "7 6", "1 5", "3 4", "2 3"))
	// Truncate Table
	tk.MustExec("truncate table t")
	tk.MustQuery("select * from t").Check(testkit.Rows())
	tk.MustExec("insert t values (1, 2)")
	tk.MustQuery("select * from t").Check(testkit.Rows("1 2"))
	tk.MustExec("truncate table t")
	tk.MustExec("insert t values (3, 4)")
	tk.MustQuery("select * from t").Check(testkit.Rows("3 4"))
	tk.MustExec("commit")

	tk.MustExec("drop table if exists t")
	tk.MustExec("create table t (a int, b int)")
	tk.MustExec("insert t values (2, 3), (4, 5), (6, 7)")
	tk.MustExec("begin")
	tk.MustExec("insert t values (0, 1)")
	tk.MustQuery("select * from t where b = 3").Check(testkit.Rows("2 3"))
	tk.MustExec("commit")

	tk.MustExec(`drop table if exists t;`)
	tk.MustExec(`create table t(a json, b bigint);`)
	tk.MustExec(`begin;`)
	tk.MustExec(`insert into t values("\"1\"", 1);`)
	tk.MustQuery(`select * from t`).Check(testkit.Rows(`"1" 1`))
	tk.MustExec(`commit;`)

	tk.MustExec(`drop table if exists t`)
	tk.MustExec("create table t(a int, b int, c int, d int, index idx(c, d))")
	tk.MustExec("begin")
	tk.MustExec("insert into t values(1, 2, 3, 4)")
	tk.MustQuery("select * from t use index(idx) where c > 1 and d = 4").Check(testkit.Rows("1 2 3 4"))
	tk.MustExec("commit")

	// Test partitioned table use wrong table ID.
	tk.MustExec(`drop table if exists t`)
	tk.MustExec(`CREATE TABLE t (c1 smallint(6) NOT NULL, c2 char(5) DEFAULT NULL) PARTITION BY RANGE ( c1 ) (
			PARTITION p0 VALUES LESS THAN (10),
			PARTITION p1 VALUES LESS THAN (20),
			PARTITION p2 VALUES LESS THAN (30),
			PARTITION p3 VALUES LESS THAN (MAXVALUE)
	)`)
	tk.MustExec("begin")
	tk.MustExec("insert into t values (1, 1)")
	tk.MustQuery("select * from t").Check(testkit.Rows("1 1"))
	tk.MustQuery("select * from t where c1 < 5").Check(testkit.Rows("1 1"))
	tk.MustQuery("select c2 from t").Check(testkit.Rows("1"))
	tk.MustExec("commit")
}

func (s *testSuite4) TestUnionScanWithCastCondition(c *C) {
	tk := testkit.NewTestKit(c, s.store)
	tk.MustExec("use test")
	tk.MustExec("create table ta (a varchar(20))")
	tk.MustExec("insert ta values ('1'), ('2')")
	tk.MustExec("create table tb (a varchar(20))")
	tk.MustExec("begin")
	tk.MustQuery("select * from ta where a = 1").Check(testkit.Rows("1"))
	tk.MustExec("insert tb values ('0')")
	tk.MustQuery("select * from ta where a = 1").Check(testkit.Rows("1"))
	tk.MustExec("rollback")
}
