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

package cascades

import (
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/expression"
	"github.com/pingcap/tidb/planner/property"
)

func (s *testCascadesSuite) TestGetEnforcerRules(c *C) {
	prop := &property.PhysicalProperty{}
	enforcers := GetEnforcerRules(prop)
	c.Assert(enforcers, IsNil)
	col := &expression.Column{}
	prop.Items = append(prop.Items, property.Item{Col: col})
	enforcers = GetEnforcerRules(prop)
	c.Assert(enforcers, NotNil)
	c.Assert(len(enforcers), Equals, 1)
	_, ok := enforcers[0].(*OrderEnforcer)
	c.Assert(ok, IsTrue)
}

func (s *testCascadesSuite) TestNewProperties(c *C) {
	prop := &property.PhysicalProperty{}
	col := &expression.Column{}
	prop.Items = append(prop.Items, property.Item{Col: col})
	enforcers := GetEnforcerRules(prop)
	orderEnforcer, _ := enforcers[0].(*OrderEnforcer)
	newProp := orderEnforcer.NewProperty(prop)
	c.Assert(newProp.Items, IsNil)
}
