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

package kv

import (
	. "github.com/pingcap/check"
)

var _ = Suite(testUtilsSuite{})

type testUtilsSuite struct {
}

func (s testUtilsSuite) TestIncInt64(c *C) {
	mb := NewMemDbBuffer(DefaultTxnMembufCap)
	key := Key("key")
	v, err := IncInt64(mb, key, 1)
	c.Check(err, IsNil)
	c.Check(v, Equals, int64(1))
	v, err = IncInt64(mb, key, 10)
	c.Check(err, IsNil)
	c.Check(v, Equals, int64(11))

	err = mb.Set(key, []byte("not int"))
	c.Check(err, IsNil)
	_, err = IncInt64(mb, key, 1)
	c.Check(err, NotNil)
}

func (s testUtilsSuite) TestGetInt64(c *C) {
	mb := NewMemDbBuffer(DefaultTxnMembufCap)
	key := Key("key")
	v, err := GetInt64(mb, key)
	c.Check(v, Equals, int64(0))
	c.Check(err, IsNil)

	_, err = IncInt64(mb, key, 15)
	c.Check(err, IsNil)
	v, err = GetInt64(mb, key)
	c.Check(v, Equals, int64(15))
	c.Check(err, IsNil)
}
