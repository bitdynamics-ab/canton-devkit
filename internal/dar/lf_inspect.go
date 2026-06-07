package dar

import (
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// Deep Daml-LF inspection. The .dalf envelope parser
// (dalf.go) stops at the package id + LF version. This walks one level
// deeper — into the Daml-LF 2 Package message — to surface each module's
// templates (with their choices), interfaces (choices + methods), and
// data types. Like dalf.go it scans the protobuf wire format directly
// (no generated bindings), using the field numbers from the stable
// daml_lf2.proto (com/daml/daml_lf_2_1):
//
//	Package: modules=1, interned_strings=2, interned_dotted_names=3
//	InternedDottedName: segments_interned_str=1 (repeated int32)
//	Module: name_interned_dname=1, data_types=4, templates=6, interfaces=8
//	DefDataType: name_interned_dname=2
//	DefTemplate: tycon_interned_dname=1, choices=6
//	TemplateChoice: name_interned_str=2
//	DefInterface: tycon_interned_dname=2, methods=3, choices=5
//	InterfaceMethod: method_interned_name=2
//
// Names are interned: a DottedName is a list of int32 indices into the
// package's interned_strings table, via the interned_dotted_names table.

// PackageContents is the deep view of one Daml-LF package: its modules,
// each with the templates / interfaces / data types it defines.
type PackageContents struct {
	Modules []ModuleContents `json:"modules"`
}

// ModuleContents is one module's declared templates, interfaces, and
// data types (record/variant/enum names).
type ModuleContents struct {
	Name       string             `json:"name"`
	Templates  []TemplateContents `json:"templates,omitempty"`
	Interfaces []IfaceContents    `json:"interfaces,omitempty"`
	DataTypes  []string           `json:"data_types,omitempty"`
}

type TemplateContents struct {
	Name    string   `json:"name"`
	Choices []string `json:"choices,omitempty"`
}

type IfaceContents struct {
	Name    string   `json:"name"`
	Choices []string `json:"choices,omitempty"`
	Methods []string `json:"methods,omitempty"`
}

// Field numbers (see comment above).
const (
	pkgFieldModules      = 1
	pkgFieldInternedStrs = 2
	pkgFieldInternedDNs  = 3

	dnFieldSegments = 1

	modFieldNameDName  = 1
	modFieldDataTypes  = 4
	modFieldTemplates  = 6
	modFieldInterfaces = 8

	dtFieldNameDName = 2

	tplFieldTyconDName = 1
	tplFieldChoices    = 6

	choiceFieldNameStr = 2

	ifaceFieldTyconDName = 2
	ifaceFieldMethods    = 3
	ifaceFieldChoices    = 5

	methodFieldNameStr = 2
)

// InspectDalf deep-parses one .dalf's bytes into its module/template/
// choice/interface structure. Returns ok=false (nil, no error) for an
// LF1-only or unparseable archive — deep inspection is best-effort and
// never fails the surrounding `dar info`.
func InspectDalf(dalf []byte) (*PackageContents, bool) {
	pkg, ok := extractLF2Package(dalf)
	if !ok {
		return nil, false
	}
	contents, err := inspectPackage(pkg)
	if err != nil || contents == nil || len(contents.Modules) == 0 {
		return nil, false
	}
	return contents, true
}

// extractLF2Package pulls the serialised Daml-LF 2 Package bytes out of a
// .dalf: Archive(field 3 = payload) → ArchivePayload(field 4 = daml_lf_2).
// Returns ok=false for an LF1-only or unparseable archive (we only deep-
// inspect LF2, which is every Splice V2-era DAR).
func extractLF2Package(dalf []byte) ([]byte, bool) {
	payload, ok := consumeBytesField(dalf, archiveFieldPayload)
	if !ok {
		return nil, false
	}
	return consumeBytesField(payload, payloadFieldDamlLf2)
}

// inspectPackage walks a Daml-LF 2 Package message. interned tables and
// modules can appear in any order, so we collect the raw module byte-
// slices + the interned tables in one scan, then resolve names.
func inspectPackage(pkg []byte) (*PackageContents, error) {
	var strs []string
	var dnames [][]int32 // each: segment indices into strs
	var modules [][]byte

	data := pkg
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, fmt.Errorf("package tag: %w", protowire.ParseError(n))
		}
		data = data[n:]
		switch int(num) {
		case pkgFieldInternedStrs:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return nil, fmt.Errorf("interned string: %w", protowire.ParseError(n2))
			}
			strs = append(strs, string(v))
			data = data[n2:]
		case pkgFieldInternedDNs:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return nil, fmt.Errorf("interned dname: %w", protowire.ParseError(n2))
			}
			dnames = append(dnames, parseInternedDottedName(v))
			data = data[n2:]
		case pkgFieldModules:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return nil, fmt.Errorf("module: %w", protowire.ParseError(n2))
			}
			modules = append(modules, append([]byte(nil), v...))
			data = data[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return nil, fmt.Errorf("skip package field %d: %w", num, protowire.ParseError(n2))
			}
			data = data[n2:]
		}
	}

	resolveDName := func(idx int32) string {
		if idx < 0 || int(idx) >= len(dnames) {
			return ""
		}
		segs := dnames[idx]
		parts := make([]string, 0, len(segs))
		for _, s := range segs {
			if s >= 0 && int(s) < len(strs) {
				parts = append(parts, strs[s])
			}
		}
		return strings.Join(parts, ".")
	}
	resolveStr := func(idx int32) string {
		if idx >= 0 && int(idx) < len(strs) {
			return strs[idx]
		}
		return ""
	}

	out := &PackageContents{}
	for _, mod := range modules {
		mc, err := parseModule(mod, resolveDName, resolveStr)
		if err != nil {
			return nil, err
		}
		out.Modules = append(out.Modules, mc)
	}
	sort.Slice(out.Modules, func(i, j int) bool { return out.Modules[i].Name < out.Modules[j].Name })
	return out, nil
}

func parseModule(mod []byte, rdn func(int32) string, rstr func(int32) string) (ModuleContents, error) {
	var mc ModuleContents
	// name_interned_dname is an int32; proto3 omits it on the wire when
	// it's 0 (the module at dotted-name index 0), so resolve the varint
	// with a 0 default rather than reading it inside the loop.
	mc.Name = rdn(int32(firstVarint(mod, modFieldNameDName)))
	data := mod
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return mc, fmt.Errorf("module tag: %w", protowire.ParseError(n))
		}
		data = data[n:]
		switch int(num) {
		case modFieldNameDName:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return mc, fmt.Errorf("module name: %w", protowire.ParseError(n2))
			}
			data = data[n2:]
		case modFieldTemplates:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return mc, fmt.Errorf("template: %w", protowire.ParseError(n2))
			}
			mc.Templates = append(mc.Templates, parseTemplate(v, rdn, rstr))
			data = data[n2:]
		case modFieldInterfaces:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return mc, fmt.Errorf("interface: %w", protowire.ParseError(n2))
			}
			mc.Interfaces = append(mc.Interfaces, parseInterface(v, rdn, rstr))
			data = data[n2:]
		case modFieldDataTypes:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return mc, fmt.Errorf("data type: %w", protowire.ParseError(n2))
			}
			if name := dataTypeName(v, rdn); name != "" {
				mc.DataTypes = append(mc.DataTypes, name)
			}
			data = data[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return mc, fmt.Errorf("skip module field %d: %w", num, protowire.ParseError(n2))
			}
			data = data[n2:]
		}
	}
	sort.Strings(mc.DataTypes)
	return mc, nil
}

func parseTemplate(t []byte, rdn func(int32) string, rstr func(int32) string) TemplateContents {
	var tc TemplateContents
	tc.Name = rdn(int32(firstVarint(t, tplFieldTyconDName)))
	data := t
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return tc
		}
		data = data[n:]
		switch int(num) {
		case tplFieldChoices:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return tc
			}
			if c := choiceName(v, rstr); c != "" {
				tc.Choices = append(tc.Choices, c)
			}
			data = data[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return tc
			}
			data = data[n2:]
		}
	}
	sort.Strings(tc.Choices)
	return tc
}

func parseInterface(i []byte, rdn func(int32) string, rstr func(int32) string) IfaceContents {
	var ic IfaceContents
	ic.Name = rdn(int32(firstVarint(i, ifaceFieldTyconDName)))
	data := i
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return ic
		}
		data = data[n:]
		switch int(num) {
		case ifaceFieldChoices:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return ic
			}
			if c := choiceName(v, rstr); c != "" {
				ic.Choices = append(ic.Choices, c)
			}
			data = data[n2:]
		case ifaceFieldMethods:
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return ic
			}
			if m := internedStrField(v, methodFieldNameStr, rstr); m != "" {
				ic.Methods = append(ic.Methods, m)
			}
			data = data[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return ic
			}
			data = data[n2:]
		}
	}
	sort.Strings(ic.Choices)
	sort.Strings(ic.Methods)
	return ic
}

func dataTypeName(dt []byte, rdn func(int32) string) string {
	// name_interned_dname defaults to 0 (proto3 omits the zero value).
	return rdn(int32(firstVarint(dt, dtFieldNameDName)))
}

func choiceName(c []byte, rstr func(int32) string) string {
	return internedStrField(c, choiceFieldNameStr, rstr)
}

// internedStrField resolves a single int32 field `field` in `msg`
// through the interned-strings table, defaulting to index 0 when the
// field is absent (proto3 omits zero-valued scalars on the wire).
func internedStrField(msg []byte, field int, rstr func(int32) string) string {
	return rstr(int32(firstVarint(msg, field)))
}

// parseInternedDottedName reads InternedDottedName.segments_interned_str
// (field 1, repeated int32) — packed or unpacked.
func parseInternedDottedName(dn []byte) []int32 {
	var out []int32
	data := dn
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return out
		}
		data = data[n:]
		if int(num) != dnFieldSegments {
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return out
			}
			data = data[n2:]
			continue
		}
		switch typ {
		case protowire.BytesType: // packed
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return out
			}
			for len(v) > 0 {
				x, m := protowire.ConsumeVarint(v)
				if m < 0 {
					break
				}
				out = append(out, int32(x))
				v = v[m:]
			}
			data = data[n2:]
		case protowire.VarintType: // unpacked
			v, n2 := protowire.ConsumeVarint(data)
			if n2 < 0 {
				return out
			}
			out = append(out, int32(v))
			data = data[n2:]
		default:
			n2 := protowire.ConsumeFieldValue(num, typ, data)
			if n2 < 0 {
				return out
			}
			data = data[n2:]
		}
	}
	return out
}

// --- small wire helpers ---------------------------------------------

// consumeBytesField returns the first LEN-typed field `field` in msg.
func consumeBytesField(msg []byte, field int) ([]byte, bool) {
	data := msg
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, false
		}
		data = data[n:]
		if int(num) == field && typ == protowire.BytesType {
			v, n2 := protowire.ConsumeBytes(data)
			if n2 < 0 {
				return nil, false
			}
			return append([]byte(nil), v...), true
		}
		n2 := protowire.ConsumeFieldValue(num, typ, data)
		if n2 < 0 {
			return nil, false
		}
		data = data[n2:]
	}
	return nil, false
}

// firstVarint returns the first varint-typed field `field` in msg, or 0
// when absent — the proto3 default for an omitted scalar.
func firstVarint(msg []byte, field int) uint64 {
	v, _ := varintField(msg, field)
	return v
}

// varintField returns the first varint-typed field `field` in msg.
func varintField(msg []byte, field int) (uint64, bool) {
	data := msg
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return 0, false
		}
		data = data[n:]
		if int(num) == field && typ == protowire.VarintType {
			v, n2 := protowire.ConsumeVarint(data)
			if n2 < 0 {
				return 0, false
			}
			return v, true
		}
		n2 := protowire.ConsumeFieldValue(num, typ, data)
		if n2 < 0 {
			return 0, false
		}
		data = data[n2:]
	}
	return 0, false
}
