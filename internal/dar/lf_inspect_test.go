package dar

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

// buildLF2Fixture hand-encodes a minimal Daml-LF 2 Package:
//
//	module "MyMod" {
//	  template MyTemplate { choice MyChoice }
//	  interface MyIface { method getX; choice IfaceChoice }
//	  data MyRecord
//	}
//
// using the documented field numbers — so the test pins the parser
// against the real wire layout without needing a 1 MB DAR checked in.
func buildLF2Fixture() []byte {
	// interned_strings (field 2), indices:
	// 0 MyMod, 1 MyTemplate, 2 MyChoice, 3 MyIface, 4 getX, 5 IfaceChoice, 6 MyRecord
	strs := []string{"MyMod", "MyTemplate", "MyChoice", "MyIface", "getX", "IfaceChoice", "MyRecord"}
	var pkg []byte
	for _, s := range strs {
		pkg = protowire.AppendTag(pkg, pkgFieldInternedStrs, protowire.BytesType)
		pkg = protowire.AppendString(pkg, s)
	}
	// interned_dotted_names (field 3): each a single-segment dname.
	// dn[i] -> segments [i] so dname index i resolves to strs[i].
	for i := range strs {
		var dn []byte
		dn = protowire.AppendTag(dn, dnFieldSegments, protowire.VarintType)
		dn = protowire.AppendVarint(dn, uint64(i))
		pkg = protowire.AppendTag(pkg, pkgFieldInternedDNs, protowire.BytesType)
		pkg = protowire.AppendBytes(pkg, dn)
	}

	// choice MyChoice (name_interned_str = 2)
	var choice []byte
	choice = protowire.AppendTag(choice, choiceFieldNameStr, protowire.VarintType)
	choice = protowire.AppendVarint(choice, 2)
	// template MyTemplate (tycon dname = 1) with the choice (field 6)
	var tpl []byte
	tpl = protowire.AppendTag(tpl, tplFieldTyconDName, protowire.VarintType)
	tpl = protowire.AppendVarint(tpl, 1)
	tpl = protowire.AppendTag(tpl, tplFieldChoices, protowire.BytesType)
	tpl = protowire.AppendBytes(tpl, choice)

	// interface MyIface (tycon dname = 3): method getX (str 4), choice IfaceChoice (str 5)
	var method []byte
	method = protowire.AppendTag(method, methodFieldNameStr, protowire.VarintType)
	method = protowire.AppendVarint(method, 4)
	var ichoice []byte
	ichoice = protowire.AppendTag(ichoice, choiceFieldNameStr, protowire.VarintType)
	ichoice = protowire.AppendVarint(ichoice, 5)
	var iface []byte
	iface = protowire.AppendTag(iface, ifaceFieldTyconDName, protowire.VarintType)
	iface = protowire.AppendVarint(iface, 3)
	iface = protowire.AppendTag(iface, ifaceFieldMethods, protowire.BytesType)
	iface = protowire.AppendBytes(iface, method)
	iface = protowire.AppendTag(iface, ifaceFieldChoices, protowire.BytesType)
	iface = protowire.AppendBytes(iface, ichoice)

	// data MyRecord (name dname = 6)
	var dt []byte
	dt = protowire.AppendTag(dt, dtFieldNameDName, protowire.VarintType)
	dt = protowire.AppendVarint(dt, 6)

	// module "MyMod" (name dname = 0)
	var mod []byte
	mod = protowire.AppendTag(mod, modFieldNameDName, protowire.VarintType)
	mod = protowire.AppendVarint(mod, 0)
	mod = protowire.AppendTag(mod, modFieldTemplates, protowire.BytesType)
	mod = protowire.AppendBytes(mod, tpl)
	mod = protowire.AppendTag(mod, modFieldInterfaces, protowire.BytesType)
	mod = protowire.AppendBytes(mod, iface)
	mod = protowire.AppendTag(mod, modFieldDataTypes, protowire.BytesType)
	mod = protowire.AppendBytes(mod, dt)

	pkg = protowire.AppendTag(pkg, pkgFieldModules, protowire.BytesType)
	pkg = protowire.AppendBytes(pkg, mod)
	return pkg
}

func TestInspectPackage_ResolvesNames(t *testing.T) {
	c, err := inspectPackage(buildLF2Fixture())
	if err != nil {
		t.Fatalf("inspectPackage: %v", err)
	}
	if len(c.Modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(c.Modules))
	}
	m := c.Modules[0]
	if m.Name != "MyMod" {
		t.Errorf("module name = %q, want MyMod", m.Name)
	}
	if len(m.Templates) != 1 || m.Templates[0].Name != "MyTemplate" {
		t.Fatalf("templates = %+v", m.Templates)
	}
	if len(m.Templates[0].Choices) != 1 || m.Templates[0].Choices[0] != "MyChoice" {
		t.Errorf("template choices = %+v, want [MyChoice]", m.Templates[0].Choices)
	}
	if len(m.Interfaces) != 1 || m.Interfaces[0].Name != "MyIface" {
		t.Fatalf("interfaces = %+v", m.Interfaces)
	}
	if len(m.Interfaces[0].Methods) != 1 || m.Interfaces[0].Methods[0] != "getX" {
		t.Errorf("iface methods = %+v, want [getX]", m.Interfaces[0].Methods)
	}
	if len(m.Interfaces[0].Choices) != 1 || m.Interfaces[0].Choices[0] != "IfaceChoice" {
		t.Errorf("iface choices = %+v, want [IfaceChoice]", m.Interfaces[0].Choices)
	}
	if len(m.DataTypes) != 1 || m.DataTypes[0] != "MyRecord" {
		t.Errorf("data types = %+v, want [MyRecord]", m.DataTypes)
	}
}

func TestParseInternedDottedName_Packed(t *testing.T) {
	// packed repeated int32 [10, 20] in one LEN field.
	var packed []byte
	packed = protowire.AppendVarint(packed, 10)
	packed = protowire.AppendVarint(packed, 20)
	var dn []byte
	dn = protowire.AppendTag(dn, dnFieldSegments, protowire.BytesType)
	dn = protowire.AppendBytes(dn, packed)
	got := parseInternedDottedName(dn)
	if len(got) != 2 || got[0] != 10 || got[1] != 20 {
		t.Errorf("packed segments = %v, want [10 20]", got)
	}
}

func TestInspectDalf_RejectsGarbage(t *testing.T) {
	if _, ok := InspectDalf([]byte("not a dalf")); ok {
		t.Error("expected ok=false for non-DAR bytes")
	}
}
