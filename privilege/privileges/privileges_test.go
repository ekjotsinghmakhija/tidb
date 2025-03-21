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

package privileges_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	. "github.com/pingcap/check"
	"github.com/pingcap/parser/auth"
	"github.com/pingcap/parser/mysql"
	"github.com/pingcap/parser/terror"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/planner/core"
	"github.com/pingcap/tidb/privilege"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/sessionctx"
	"github.com/pingcap/tidb/store/mockstore"
	"github.com/pingcap/tidb/util/testleak"
	"github.com/pingcap/tidb/util/testutil"
)

func TestT(t *testing.T) {
	CustomVerboseFlag = true
	TestingT(t)
}

var _ = Suite(&testPrivilegeSuite{})

type testPrivilegeSuite struct {
	store  kv.Storage
	dom    *domain.Domain
	dbName string

	createDBSQL              string
	createDB1SQL             string
	dropDBSQL                string
	useDBSQL                 string
	createTableSQL           string
	createSystemDBSQL        string
	createUserTableSQL       string
	createDBPrivTableSQL     string
	createTablePrivTableSQL  string
	createColumnPrivTableSQL string
}

func (s *testPrivilegeSuite) SetUpSuite(c *C) {
	testleak.BeforeTest()
	s.dbName = "test"
	s.dom, s.store = newStore(c, s.dbName)
}

func (s *testPrivilegeSuite) TearDownSuite(c *C) {
	s.dom.Close()
	s.store.Close()
	testleak.AfterTest(c)()
}

func (s *testPrivilegeSuite) SetUpTest(c *C) {
	se := newSession(c, s.store, s.dbName)
	s.createDBSQL = fmt.Sprintf("create database if not exists %s;", s.dbName)
	s.createDB1SQL = fmt.Sprintf("create database if not exists %s1;", s.dbName)
	s.dropDBSQL = fmt.Sprintf("drop database if exists %s;", s.dbName)
	s.useDBSQL = fmt.Sprintf("use %s;", s.dbName)
	s.createTableSQL = `CREATE TABLE test(id INT NOT NULL DEFAULT 1, name varchar(255), PRIMARY KEY(id));`

	mustExec(c, se, s.createDBSQL)
	mustExec(c, se, s.createDB1SQL) // create database test1
	mustExec(c, se, s.useDBSQL)
	mustExec(c, se, s.createTableSQL)

	s.createSystemDBSQL = fmt.Sprintf("create database if not exists %s;", mysql.SystemDB)
	s.createUserTableSQL = session.CreateUserTable
	s.createDBPrivTableSQL = session.CreateDBPrivTable
	s.createTablePrivTableSQL = session.CreateTablePrivTable
	s.createColumnPrivTableSQL = session.CreateColumnPrivTable

	mustExec(c, se, s.createSystemDBSQL)
	mustExec(c, se, s.createUserTableSQL)
	mustExec(c, se, s.createDBPrivTableSQL)
	mustExec(c, se, s.createTablePrivTableSQL)
	mustExec(c, se, s.createColumnPrivTableSQL)
}

func (s *testPrivilegeSuite) TearDownTest(c *C) {
	// drop db
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, s.dropDBSQL)
}

func (s *testPrivilegeSuite) TestCheckDBPrivilege(c *C) {
	rootSe := newSession(c, s.store, s.dbName)
	mustExec(c, rootSe, `CREATE USER 'testcheck'@'localhost';`)
	mustExec(c, rootSe, `CREATE USER 'testcheck_tmp'@'localhost';`)

	se := newSession(c, s.store, s.dbName)
	activeRoles := make([]*auth.RoleIdentity, 0)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "testcheck", Hostname: "localhost"}, nil, nil), IsTrue)
	pc := privilege.GetPrivilegeManager(se)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.SelectPriv), IsFalse)

	mustExec(c, rootSe, `GRANT SELECT ON *.* TO  'testcheck'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.UpdatePriv), IsFalse)

	mustExec(c, rootSe, `GRANT Update ON test.* TO  'testcheck'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.UpdatePriv), IsTrue)

	activeRoles = append(activeRoles, &auth.RoleIdentity{Username: "testcheck", Hostname: "localhost"})
	mustExec(c, rootSe, `GRANT 'testcheck'@'localhost' TO 'testcheck_tmp'@'localhost';`)
	se2 := newSession(c, s.store, s.dbName)
	c.Assert(se2.Auth(&auth.UserIdentity{Username: "testcheck_tmp", Hostname: "localhost"}, nil, nil), IsTrue)
	pc = privilege.GetPrivilegeManager(se2)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.UpdatePriv), IsTrue)
}

func (s *testPrivilegeSuite) TestCheckTablePrivilege(c *C) {
	rootSe := newSession(c, s.store, s.dbName)
	mustExec(c, rootSe, `CREATE USER 'test1'@'localhost';`)
	mustExec(c, rootSe, `CREATE USER 'test1_tmp'@'localhost';`)

	se := newSession(c, s.store, s.dbName)
	activeRoles := make([]*auth.RoleIdentity, 0)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "test1", Hostname: "localhost"}, nil, nil), IsTrue)
	pc := privilege.GetPrivilegeManager(se)
	c.Assert(pc.RequestVerification(activeRoles, "test", "test", "", mysql.SelectPriv), IsFalse)

	mustExec(c, rootSe, `GRANT SELECT ON *.* TO  'test1'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "test", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "test", "", mysql.UpdatePriv), IsFalse)

	mustExec(c, rootSe, `GRANT Update ON test.* TO  'test1'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "test", "", mysql.UpdatePriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "test", "", mysql.IndexPriv), IsFalse)

	activeRoles = append(activeRoles, &auth.RoleIdentity{Username: "test1", Hostname: "localhost"})
	se2 := newSession(c, s.store, s.dbName)
	mustExec(c, rootSe, `GRANT 'test1'@'localhost' TO 'test1_tmp'@'localhost';`)
	c.Assert(se2.Auth(&auth.UserIdentity{Username: "test1_tmp", Hostname: "localhost"}, nil, nil), IsTrue)
	pc2 := privilege.GetPrivilegeManager(se2)
	c.Assert(pc2.RequestVerification(activeRoles, "test", "test", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc2.RequestVerification(activeRoles, "test", "test", "", mysql.UpdatePriv), IsTrue)
	c.Assert(pc2.RequestVerification(activeRoles, "test", "test", "", mysql.IndexPriv), IsFalse)

	mustExec(c, rootSe, `GRANT Index ON test.test TO  'test1'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "test", "", mysql.IndexPriv), IsTrue)
	c.Assert(pc2.RequestVerification(activeRoles, "test", "test", "", mysql.IndexPriv), IsTrue)
}

func (s *testPrivilegeSuite) TestCheckViewPrivilege(c *C) {
	rootSe := newSession(c, s.store, s.dbName)
	mustExec(c, rootSe, `CREATE USER 'vuser'@'localhost';`)
	mustExec(c, rootSe, `CREATE VIEW v AS SELECT * FROM test;`)

	se := newSession(c, s.store, s.dbName)
	activeRoles := make([]*auth.RoleIdentity, 0)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "vuser", Hostname: "localhost"}, nil, nil), IsTrue)
	pc := privilege.GetPrivilegeManager(se)
	c.Assert(pc.RequestVerification(activeRoles, "test", "v", "", mysql.SelectPriv), IsFalse)

	mustExec(c, rootSe, `GRANT SELECT ON test.v TO 'vuser'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "v", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "v", "", mysql.ShowViewPriv), IsFalse)

	mustExec(c, rootSe, `GRANT SHOW VIEW ON test.v TO 'vuser'@'localhost';`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "v", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "v", "", mysql.ShowViewPriv), IsTrue)
}

func (s *testPrivilegeSuite) TestCheckPrivilegeWithRoles(c *C) {
	rootSe := newSession(c, s.store, s.dbName)
	mustExec(c, rootSe, `CREATE USER 'test_role'@'localhost';`)
	mustExec(c, rootSe, `CREATE ROLE r_1, r_2, r_3;`)
	mustExec(c, rootSe, `GRANT r_1, r_2, r_3 TO 'test_role'@'localhost';`)

	se := newSession(c, s.store, s.dbName)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "test_role", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `SET ROLE r_1, r_2;`)
	mustExec(c, rootSe, `SET DEFAULT ROLE r_1 TO 'test_role'@'localhost';`)

	mustExec(c, rootSe, `GRANT SELECT ON test.* TO r_1;`)
	pc := privilege.GetPrivilegeManager(se)
	activeRoles := se.GetSessionVars().ActiveRoles
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.SelectPriv), IsTrue)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.UpdatePriv), IsFalse)
	mustExec(c, rootSe, `GRANT UPDATE ON test.* TO r_2;`)
	c.Assert(pc.RequestVerification(activeRoles, "test", "", "", mysql.UpdatePriv), IsTrue)

	mustExec(c, se, `flush privileges`)
	mustExec(c, se, `SET ROLE NONE;`)
	c.Assert(len(se.GetSessionVars().ActiveRoles), Equals, 0)
	mustExec(c, se, `SET ROLE DEFAULT;`)
	c.Assert(len(se.GetSessionVars().ActiveRoles), Equals, 1)
	mustExec(c, se, `SET ROLE ALL;`)
	c.Assert(len(se.GetSessionVars().ActiveRoles), Equals, 3)
	mustExec(c, se, `SET ROLE ALL EXCEPT r_1, r_2;`)
	c.Assert(len(se.GetSessionVars().ActiveRoles), Equals, 1)
}

func (s *testPrivilegeSuite) TestShowGrants(c *C) {
	se := newSession(c, s.store, s.dbName)
	ctx, _ := se.(sessionctx.Context)
	mustExec(c, se, `CREATE USER 'show'@'localhost' identified by '123';`)
	mustExec(c, se, `GRANT Index ON *.* TO  'show'@'localhost';`)
	pc := privilege.GetPrivilegeManager(se)

	gs, err := pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 1)
	c.Assert(gs[0], Equals, `GRANT Index ON *.* TO 'show'@'localhost'`)

	mustExec(c, se, `GRANT Select ON *.* TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 1)
	c.Assert(gs[0], Equals, `GRANT Select,Index ON *.* TO 'show'@'localhost'`)

	// The order of privs is the same with AllGlobalPrivs
	mustExec(c, se, `GRANT Update ON *.* TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 1)
	c.Assert(gs[0], Equals, `GRANT Select,Update,Index ON *.* TO 'show'@'localhost'`)

	// All privileges
	mustExec(c, se, `GRANT ALL ON *.* TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 1)
	c.Assert(gs[0], Equals, `GRANT ALL PRIVILEGES ON *.* TO 'show'@'localhost'`)

	// Add db scope privileges
	mustExec(c, se, `GRANT Select ON test.* TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 2)
	expected := []string{`GRANT ALL PRIVILEGES ON *.* TO 'show'@'localhost'`,
		`GRANT Select ON test.* TO 'show'@'localhost'`}
	c.Assert(testutil.CompareUnorderedStringSlice(gs, expected), IsTrue)

	mustExec(c, se, `GRANT Index ON test1.* TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 3)
	expected = []string{`GRANT ALL PRIVILEGES ON *.* TO 'show'@'localhost'`,
		`GRANT Select ON test.* TO 'show'@'localhost'`,
		`GRANT Index ON test1.* TO 'show'@'localhost'`}
	c.Assert(testutil.CompareUnorderedStringSlice(gs, expected), IsTrue)

	mustExec(c, se, `GRANT ALL ON test1.* TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 3)
	expected = []string{`GRANT ALL PRIVILEGES ON *.* TO 'show'@'localhost'`,
		`GRANT Select ON test.* TO 'show'@'localhost'`,
		`GRANT ALL PRIVILEGES ON test1.* TO 'show'@'localhost'`}
	c.Assert(testutil.CompareUnorderedStringSlice(gs, expected), IsTrue)

	// Add table scope privileges
	mustExec(c, se, `GRANT Update ON test.test TO  'show'@'localhost';`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 4)
	expected = []string{`GRANT ALL PRIVILEGES ON *.* TO 'show'@'localhost'`,
		`GRANT Select ON test.* TO 'show'@'localhost'`,
		`GRANT ALL PRIVILEGES ON test1.* TO 'show'@'localhost'`,
		`GRANT Update ON test.test TO 'show'@'localhost'`}
	c.Assert(testutil.CompareUnorderedStringSlice(gs, expected), IsTrue)

	// Expected behavior: Usage still exists after revoking all privileges
	mustExec(c, se, `REVOKE ALL PRIVILEGES ON *.* FROM 'show'@'localhost'`)
	mustExec(c, se, `REVOKE Select on test.* FROM 'show'@'localhost'`)
	mustExec(c, se, `REVOKE ALL ON test1.* FROM 'show'@'localhost'`)
	mustExec(c, se, `REVOKE UPDATE on test.test FROM 'show'@'localhost'`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 1)
	c.Assert(gs[0], Equals, `GRANT USAGE ON *.* TO 'show'@'localhost'`)

	// Usage should not exist after dropping the user
	// Which we need privileges to do so!
	ctx.GetSessionVars().User = &auth.UserIdentity{Username: "root", Hostname: "localhost"}
	mustExec(c, se, `DROP USER 'show'@'localhost'`)

	// This should now return an error
	_, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "show", Hostname: "localhost"}, nil)
	c.Assert(err, NotNil)
	// cant show grants for non-existent
	errNonexistingGrant := terror.ClassPrivilege.New(mysql.ErrNonexistingGrant, mysql.MySQLErrName[mysql.ErrNonexistingGrant])
	c.Assert(terror.ErrorEqual(err, errNonexistingGrant), IsTrue)

	// Test SHOW GRANTS with USING roles.
	mustExec(c, se, `CREATE ROLE 'r1', 'r2'`)
	mustExec(c, se, `GRANT SELECT ON test.* TO 'r1'`)
	mustExec(c, se, `GRANT INSERT, UPDATE ON test.* TO 'r2'`)
	mustExec(c, se, `CREATE USER 'testrole'@'localhost' IDENTIFIED BY 'u1pass'`)
	mustExec(c, se, `GRANT 'r1', 'r2' TO 'testrole'@'localhost'`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "testrole", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 2)
	roles := make([]*auth.RoleIdentity, 0)
	roles = append(roles, &auth.RoleIdentity{Username: "r2", Hostname: "%"})
	mustExec(c, se, `GRANT DELETE ON test.* TO 'testrole'@'localhost'`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "testrole", Hostname: "localhost"}, roles)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 3)
	roles = append(roles, &auth.RoleIdentity{Username: "r1", Hostname: "%"})
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "testrole", Hostname: "localhost"}, roles)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 3)
	mustExec(c, se, `GRANT INSERT, DELETE ON test.test TO 'r2'`)
	mustExec(c, se, `GRANT UPDATE ON a.b TO 'testrole'@'localhost'`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "testrole", Hostname: "localhost"}, roles)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 5)
	mustExec(c, se, `DROP ROLE 'r1', 'r2'`)
	mustExec(c, se, `DROP USER 'testrole'@'localhost'`)
	mustExec(c, se, `CREATE ROLE 'r1', 'r2'`)
	mustExec(c, se, `GRANT SELECT ON test.* TO 'r2'`)
	mustExec(c, se, `CREATE USER 'testrole'@'localhost' IDENTIFIED BY 'u1pass'`)
	mustExec(c, se, `GRANT 'r1' TO 'testrole'@'localhost'`)
	mustExec(c, se, `GRANT 'r2' TO 'r1'`)
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "testrole", Hostname: "localhost"}, nil)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 2)
	roles = make([]*auth.RoleIdentity, 0)
	roles = append(roles, &auth.RoleIdentity{Username: "r1", Hostname: "%"})
	gs, err = pc.ShowGrants(se, &auth.UserIdentity{Username: "testrole", Hostname: "localhost"}, roles)
	c.Assert(err, IsNil)
	c.Assert(gs, HasLen, 3)
}

func (s *testPrivilegeSuite) TestDropTablePriv(c *C) {
	se := newSession(c, s.store, s.dbName)
	ctx, _ := se.(sessionctx.Context)
	mustExec(c, se, `CREATE TABLE todrop(c int);`)
	// ctx.GetSessionVars().User = "root@localhost"
	c.Assert(se.Auth(&auth.UserIdentity{Username: "root", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `CREATE USER 'drop'@'localhost';`)
	mustExec(c, se, `GRANT Select ON test.todrop TO  'drop'@'localhost';`)

	// ctx.GetSessionVars().User = "drop@localhost"
	c.Assert(se.Auth(&auth.UserIdentity{Username: "drop", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `SELECT * FROM todrop;`)
	_, err := se.Execute(context.Background(), "DROP TABLE todrop;")
	c.Assert(err, NotNil)

	se = newSession(c, s.store, s.dbName)
	ctx.GetSessionVars().User = &auth.UserIdentity{Username: "root", Hostname: "localhost"}
	mustExec(c, se, `GRANT Drop ON test.todrop TO  'drop'@'localhost';`)

	se = newSession(c, s.store, s.dbName)
	ctx.GetSessionVars().User = &auth.UserIdentity{Username: "drop", Hostname: "localhost"}
	mustExec(c, se, `DROP TABLE todrop;`)
}

func (s *testPrivilegeSuite) TestSetPasswdStmt(c *C) {

	se := newSession(c, s.store, s.dbName)

	// high privileged user setting password for other user (passes)
	mustExec(c, se, "CREATE USER 'superuser'")
	mustExec(c, se, "CREATE USER 'nobodyuser'")
	mustExec(c, se, "GRANT ALL ON *.* TO 'superuser'")

	c.Assert(se.Auth(&auth.UserIdentity{Username: "superuser", Hostname: "localhost", AuthUsername: "superuser", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, "SET PASSWORD for 'nobodyuser' = 'newpassword'")
	mustExec(c, se, "SET PASSWORD for 'nobodyuser' = ''")

	// low privileged user trying to set password for other user (fails)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "nobodyuser", Hostname: "localhost", AuthUsername: "nobodyuser", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), "SET PASSWORD for 'superuser' = 'newpassword'")
	c.Assert(err, NotNil)
}

func (s *testPrivilegeSuite) TestSelectViewSecurity(c *C) {
	se := newSession(c, s.store, s.dbName)
	ctx, _ := se.(sessionctx.Context)
	mustExec(c, se, `CREATE TABLE viewsecurity(c int);`)
	// ctx.GetSessionVars().User = "root@localhost"
	c.Assert(se.Auth(&auth.UserIdentity{Username: "root", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `CREATE USER 'selectusr'@'localhost';`)
	mustExec(c, se, `GRANT CREATE VIEW ON test.* TO  'selectusr'@'localhost';`)
	mustExec(c, se, `GRANT SELECT ON test.viewsecurity TO  'selectusr'@'localhost';`)

	// ctx.GetSessionVars().User = "selectusr@localhost"
	c.Assert(se.Auth(&auth.UserIdentity{Username: "selectusr", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `SELECT * FROM test.viewsecurity;`)
	mustExec(c, se, `CREATE ALGORITHM = UNDEFINED SQL SECURITY DEFINER VIEW test.selectviewsecurity as select * FROM test.viewsecurity;`)

	se = newSession(c, s.store, s.dbName)
	ctx.GetSessionVars().User = &auth.UserIdentity{Username: "root", Hostname: "localhost"}
	mustExec(c, se, "SELECT * FROM test.selectviewsecurity")
	mustExec(c, se, `REVOKE Select ON test.viewsecurity FROM  'selectusr'@'localhost';`)
	_, err := se.Execute(context.Background(), "select * from test.selectviewsecurity")
	c.Assert(err.Error(), Equals, core.ErrViewInvalid.GenWithStackByArgs("test", "selectviewsecurity").Error())
}

func (s *testPrivilegeSuite) TestRoleAdminSecurity(c *C) {
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER 'r1'@'localhost';`)
	mustExec(c, se, `CREATE USER 'r2'@'localhost';`)
	mustExec(c, se, `GRANT ALL ON *.* to r1@localhost`)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "r1", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `create role r_test1@localhost`)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "r2", Hostname: "localhost"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), `create role r_test2@localhost`)
	c.Assert(terror.ErrorEqual(err, core.ErrSpecificAccessDenied), IsTrue)
}

func (s *testPrivilegeSuite) TestCheckAuthenticate(c *C) {

	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER 'u1'@'localhost';`)
	mustExec(c, se, `CREATE USER 'u2'@'localhost' identified by 'abc';`)
	mustExec(c, se, `CREATE USER 'u3@example.com'@'localhost';`)
	mustExec(c, se, `CREATE USER u4@localhost;`)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "u1", Hostname: "localhost"}, nil, nil), IsTrue)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u2", Hostname: "localhost"}, nil, nil), IsFalse)
	salt := []byte{85, 92, 45, 22, 58, 79, 107, 6, 122, 125, 58, 80, 12, 90, 103, 32, 90, 10, 74, 82}
	authentication := []byte{24, 180, 183, 225, 166, 6, 81, 102, 70, 248, 199, 143, 91, 204, 169, 9, 161, 171, 203, 33}
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u2", Hostname: "localhost"}, authentication, salt), IsTrue)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u3@example.com", Hostname: "localhost"}, nil, nil), IsTrue)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u4", Hostname: "localhost"}, nil, nil), IsTrue)

	se1 := newSession(c, s.store, s.dbName)
	mustExec(c, se1, "drop user 'u1'@'localhost'")
	mustExec(c, se1, "drop user 'u2'@'localhost'")
	mustExec(c, se1, "drop user 'u3@example.com'@'localhost'")
	mustExec(c, se1, "drop user u4@localhost")

	c.Assert(se.Auth(&auth.UserIdentity{Username: "u1", Hostname: "localhost"}, nil, nil), IsFalse)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u2", Hostname: "localhost"}, nil, nil), IsFalse)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u3@example.com", Hostname: "localhost"}, nil, nil), IsFalse)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u4", Hostname: "localhost"}, nil, nil), IsFalse)

	se2 := newSession(c, s.store, s.dbName)
	mustExec(c, se2, "create role 'r1'@'localhost'")
	mustExec(c, se2, "create role 'r2'@'localhost'")
	mustExec(c, se2, "create role 'r3@example.com'@'localhost'")
	c.Assert(se.Auth(&auth.UserIdentity{Username: "r1", Hostname: "localhost"}, nil, nil), IsFalse)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "r2", Hostname: "localhost"}, nil, nil), IsFalse)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "r3@example.com", Hostname: "localhost"}, nil, nil), IsFalse)

	mustExec(c, se1, "drop user 'r1'@'localhost'")
	mustExec(c, se1, "drop user 'r2'@'localhost'")
	mustExec(c, se1, "drop user 'r3@example.com'@'localhost'")
}

func (s *testPrivilegeSuite) TestUseDB(c *C) {

	se := newSession(c, s.store, s.dbName)
	// high privileged user
	mustExec(c, se, "CREATE USER 'usesuper'")
	mustExec(c, se, "CREATE USER 'usenobody'")
	mustExec(c, se, "GRANT ALL ON *.* TO 'usesuper'")
	c.Assert(se.Auth(&auth.UserIdentity{Username: "usesuper", Hostname: "localhost", AuthUsername: "usesuper", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, "use mysql")
	// low privileged user
	c.Assert(se.Auth(&auth.UserIdentity{Username: "usenobody", Hostname: "localhost", AuthUsername: "usenobody", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), "use mysql")
	c.Assert(err, NotNil)

	// try again after privilege granted
	c.Assert(se.Auth(&auth.UserIdentity{Username: "usesuper", Hostname: "localhost", AuthUsername: "usesuper", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, "GRANT SELECT ON mysql.* TO 'usenobody'")
	c.Assert(se.Auth(&auth.UserIdentity{Username: "usenobody", Hostname: "localhost", AuthUsername: "usenobody", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err = se.Execute(context.Background(), "use mysql")
	c.Assert(err, IsNil)

	// test `use db` for role.
	c.Assert(se.Auth(&auth.UserIdentity{Username: "usesuper", Hostname: "localhost", AuthUsername: "usesuper", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, `CREATE DATABASE app_db`)
	mustExec(c, se, `CREATE ROLE 'app_developer'`)
	mustExec(c, se, `GRANT ALL ON app_db.* TO 'app_developer'`)
	mustExec(c, se, `CREATE USER 'dev'@'localhost'`)
	mustExec(c, se, `GRANT 'app_developer' TO 'dev'@'localhost'`)
	mustExec(c, se, `SET DEFAULT ROLE 'app_developer' TO 'dev'@'localhost'`)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "dev", Hostname: "localhost", AuthUsername: "dev", AuthHostname: "localhost"}, nil, nil), IsTrue)
	_, err = se.Execute(context.Background(), "use app_db")
	c.Assert(err, IsNil)
	_, err = se.Execute(context.Background(), "use mysql")
	c.Assert(err, NotNil)
}

func (s *testPrivilegeSuite) TestSetGlobal(c *C) {
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER setglobal_a@localhost`)
	mustExec(c, se, `CREATE USER setglobal_b@localhost`)
	mustExec(c, se, `GRANT SUPER ON *.* to setglobal_a@localhost`)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "setglobal_a", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `set global innodb_commit_concurrency=16`)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "setglobal_b", Hostname: "localhost"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), `set global innodb_commit_concurrency=16`)
	c.Assert(terror.ErrorEqual(err, core.ErrSpecificAccessDenied), IsTrue)
}

func (s *testPrivilegeSuite) TestCreateDropUser(c *C) {
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER tcd1, tcd2`)
	mustExec(c, se, `GRANT ALL ON *.* to tcd2`)

	// should fail
	c.Assert(se.Auth(&auth.UserIdentity{Username: "tcd1", Hostname: "localhost", AuthUsername: "tcd1", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), `CREATE USER acdc`)
	c.Assert(terror.ErrorEqual(err, core.ErrSpecificAccessDenied), IsTrue)
	_, err = se.Execute(context.Background(), `DROP USER tcd2`)
	c.Assert(terror.ErrorEqual(err, core.ErrSpecificAccessDenied), IsTrue)

	// should pass
	c.Assert(se.Auth(&auth.UserIdentity{Username: "tcd2", Hostname: "localhost", AuthUsername: "tcd2", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, `DROP USER tcd1`)
	mustExec(c, se, `CREATE USER tcd1`)

	// should pass
	mustExec(c, se, `GRANT tcd2 TO tcd1`)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "tcd1", Hostname: "localhost", AuthUsername: "tcd1", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, `SET ROLE tcd2;`)
	mustExec(c, se, `CREATE USER tcd3`)
	mustExec(c, se, `DROP USER tcd3`)
}

func (s *testPrivilegeSuite) TestShowCreateTable(c *C) {
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER tsct1, tsct2`)
	mustExec(c, se, `GRANT select ON mysql.* to tsct2`)

	// should fail
	c.Assert(se.Auth(&auth.UserIdentity{Username: "tsct1", Hostname: "localhost", AuthUsername: "tsct1", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), `SHOW CREATE TABLE mysql.user`)
	c.Assert(terror.ErrorEqual(err, core.ErrTableaccessDenied), IsTrue)

	// should pass
	c.Assert(se.Auth(&auth.UserIdentity{Username: "tsct2", Hostname: "localhost", AuthUsername: "tsct2", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, `SHOW CREATE TABLE mysql.user`)
}

func (s *testPrivilegeSuite) TestAnalyzeTable(c *C) {

	se := newSession(c, s.store, s.dbName)
	// high privileged user
	mustExec(c, se, "CREATE USER 'asuper'")
	mustExec(c, se, "CREATE USER 'anobody'")
	mustExec(c, se, "GRANT ALL ON *.* TO 'asuper'")
	mustExec(c, se, "CREATE DATABASE atest")
	mustExec(c, se, "use atest")
	mustExec(c, se, "CREATE TABLE t1 (a int)")

	c.Assert(se.Auth(&auth.UserIdentity{Username: "asuper", Hostname: "localhost", AuthUsername: "asuper", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, "analyze table mysql.user")
	// low privileged user
	c.Assert(se.Auth(&auth.UserIdentity{Username: "anobody", Hostname: "localhost", AuthUsername: "anobody", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), "analyze table t1")
	c.Assert(terror.ErrorEqual(err, core.ErrTableaccessDenied), IsTrue)
	c.Assert(err.Error(), Equals, "[planner:1142]INSERT command denied to user 'anobody'@'%' for table 't1'")

	_, err = se.Execute(context.Background(), "select * from t1")
	c.Assert(err.Error(), Equals, "[planner:1142]SELECT command denied to user 'anobody'@'localhost' for table 't1'")

	// try again after SELECT privilege granted
	c.Assert(se.Auth(&auth.UserIdentity{Username: "asuper", Hostname: "localhost", AuthUsername: "asuper", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, "GRANT SELECT ON atest.* TO 'anobody'")
	c.Assert(se.Auth(&auth.UserIdentity{Username: "anobody", Hostname: "localhost", AuthUsername: "anobody", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err = se.Execute(context.Background(), "analyze table t1")
	c.Assert(terror.ErrorEqual(err, core.ErrTableaccessDenied), IsTrue)
	c.Assert(err.Error(), Equals, "[planner:1142]INSERT command denied to user 'anobody'@'%' for table 't1'")
	// Add INSERT privilege and it should work.
	c.Assert(se.Auth(&auth.UserIdentity{Username: "asuper", Hostname: "localhost", AuthUsername: "asuper", AuthHostname: "%"}, nil, nil), IsTrue)
	mustExec(c, se, "GRANT INSERT ON atest.* TO 'anobody'")
	c.Assert(se.Auth(&auth.UserIdentity{Username: "anobody", Hostname: "localhost", AuthUsername: "anobody", AuthHostname: "%"}, nil, nil), IsTrue)
	_, err = se.Execute(context.Background(), "analyze table t1")
	c.Assert(err, IsNil)

}

func (s *testPrivilegeSuite) TestInformationSchema(c *C) {

	// This test tests no privilege check for INFORMATION_SCHEMA database.
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER 'u1'@'localhost';`)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "u1", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `select * from information_schema.tables`)
	mustExec(c, se, `select * from information_schema.key_column_usage`)
}

func (s *testPrivilegeSuite) TestAdminCommand(c *C) {
	se := newSession(c, s.store, s.dbName)
	c.Assert(se.Auth(&auth.UserIdentity{Username: "root", Hostname: "localhost"}, nil, nil), IsTrue)
	mustExec(c, se, `CREATE USER 'test_admin'@'localhost';`)
	mustExec(c, se, `CREATE TABLE t(a int)`)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "test_admin", Hostname: "localhost"}, nil, nil), IsTrue)
	_, err := se.Execute(context.Background(), "ADMIN SHOW DDL JOBS")
	c.Assert(strings.Contains(err.Error(), "privilege check fail"), IsTrue)
	_, err = se.Execute(context.Background(), "ADMIN CHECK TABLE t")
	c.Assert(strings.Contains(err.Error(), "privilege check fail"), IsTrue)

	c.Assert(se.Auth(&auth.UserIdentity{Username: "root", Hostname: "localhost"}, nil, nil), IsTrue)
	_, err = se.Execute(context.Background(), "ADMIN SHOW DDL JOBS")
	c.Assert(err, IsNil)
}

func (s *testPrivilegeSuite) TestGetEncodedPassword(c *C) {
	se := newSession(c, s.store, s.dbName)
	mustExec(c, se, `CREATE USER 'test_encode_u'@'localhost' identified by 'root';`)
	pc := privilege.GetPrivilegeManager(se)
	c.Assert(pc.GetEncodedPassword("test_encode_u", "localhost"), Equals, "*81F5E21E35407D884A6CD4A731AEBFB6AF209E1B")
}

func (s *testPrivilegeSuite) TestDefaultRoles(c *C) {
	rootSe := newSession(c, s.store, s.dbName)
	mustExec(c, rootSe, `CREATE USER 'testdefault'@'localhost';`)
	mustExec(c, rootSe, `CREATE ROLE 'testdefault_r1'@'localhost', 'testdefault_r2'@'localhost';`)
	mustExec(c, rootSe, `GRANT 'testdefault_r1'@'localhost', 'testdefault_r2'@'localhost' TO 'testdefault'@'localhost';`)

	se := newSession(c, s.store, s.dbName)
	pc := privilege.GetPrivilegeManager(se)

	ret := pc.GetDefaultRoles("testdefault", "localhost")
	c.Assert(len(ret), Equals, 0)

	mustExec(c, rootSe, `SET DEFAULT ROLE ALL TO 'testdefault'@'localhost';`)
	mustExec(c, rootSe, `flush privileges;`)
	ret = pc.GetDefaultRoles("testdefault", "localhost")
	c.Assert(len(ret), Equals, 2)

	mustExec(c, rootSe, `SET DEFAULT ROLE NONE TO 'testdefault'@'localhost';`)
	mustExec(c, rootSe, `flush privileges;`)
	ret = pc.GetDefaultRoles("testdefault", "localhost")
	c.Assert(len(ret), Equals, 0)
}

func mustExec(c *C, se session.Session, sql string) {
	_, err := se.Execute(context.Background(), sql)
	c.Assert(err, IsNil)
}

func newStore(c *C, dbPath string) (*domain.Domain, kv.Storage) {
	store, err := mockstore.NewMockTikvStore()
	session.SetSchemaLease(0)
	session.SetStatsLease(0)
	c.Assert(err, IsNil)
	dom, err := session.BootstrapSession(store)
	c.Assert(err, IsNil)
	return dom, store
}

func newSession(c *C, store kv.Storage, dbName string) session.Session {
	se, err := session.CreateSession4Test(store)
	c.Assert(err, IsNil)
	mustExec(c, se, "create database if not exists "+dbName)
	mustExec(c, se, "use "+dbName)
	return se
}
