// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package isolatepolicy is the pinned compiler/build policy for trusted
// metadata operations. It grants no package-wide allocation or access privilege.
package isolatepolicy

const ProtobufModule = "google.golang.org/protobuf"
const ProtobufVersion = "v1.36.11"

const TemporalAPIModule = "go.temporal.io/api"
const TemporalAPIVersion = "v1.63.6"

// MetadataScope identifies functions that only construct/cache type descriptions
// or read the built-in registries. Marshal/unmarshal, value allocation, and
// application callbacks are deliberately absent. Private or custom descriptor
// implementations are outside this initial manifest.
func MetadataScope(pkg, function string) bool {
	switch pkg {
	case ProtobufModule + "/internal/impl":
		return function == "(*MessageInfo).initOnce" || function == "(*MessageInfo).Descriptor" || function == "needsInitCheck" || function == "(*ExtensionInfo).lazyInitSlow"
	case ProtobufModule + "/internal/filedesc":
		switch function {
		case "(*File).lazyInitOnce", "(*stringName).lazyInit",
			"(*Message).Fields", "(*Fields).ByName",
			"(*Names).lazyInit", "(*EnumRanges).lazyInit", "(*FieldRanges).lazyInit",
			"(*FieldNumbers).Has", "(*OneofFields).lazyInit", "(*SourceLocations).lazyInit",
			"(*Enums).lazyInit", "(*EnumValues).lazyInit", "(*Messages).lazyInit",
			"(*Fields).lazyInit", "(*Oneofs).lazyInit", "(*Extensions).lazyInit",
			"(*Services).lazyInit", "(*Methods).lazyInit":
			return true
		}
	case ProtobufModule + "/reflect/protoregistry":
		switch function {
		case "(*Files).FindDescriptorByName", "(*Files).FindFileByPath", "(*Files).NumFiles", "(*Files).NumFilesByPackage",
			"(*Types).FindEnumByName", "(*Types).FindMessageByName", "(*Types).FindMessageByURL",
			"(*Types).FindExtensionByName", "(*Types).FindExtensionByNumber",
			"(*Types).NumEnums", "(*Types).NumMessages", "(*Types).NumExtensions", "(*Types).NumExtensionsByMessage":
			return true
		}
	}
	return false
}

// RejectedMetadata preserves host behavior and fails explicitly in an isolate.
// Registry visitors hold a process lock while invoking caller code, so granting
// the whole method a service scope would give that caller metadata privileges.
// Lazy option decoders and legacy descriptor hooks need a separate audit.
func RejectedMetadata(pkg, function string) bool {
	switch pkg {
	case ProtobufModule + "/reflect/protoregistry":
		switch function {
		case "(*Files).RegisterFile", "(*Files).RangeFiles", "(*Files).RangeFilesByPackage",
			"(*Types).RegisterEnum", "(*Types).RegisterMessage", "(*Types).RegisterExtension",
			"(*Types).RangeEnums", "(*Types).RangeMessages", "(*Types).RangeExtensions", "(*Types).RangeExtensionsByMessage":
			return true
		}
	case ProtobufModule + "/internal/impl":
		switch function {
		case "legacyLoadMessageInfo", "legacyLoadMessageDesc", "aberrantLoadMessageDesc":
			return true
		}
	case ProtobufModule + "/internal/filedesc":
		switch function {
		case "(*File).Options", "(*Enum).Options", "(*Message).Options", "(*Field).Options",
			"(*Oneof).Options", "(*Extension).Options", "(*Service).Options", "(*Method).Options", "(*File).OptionImports":
			return true
		}
	}
	return false
}
