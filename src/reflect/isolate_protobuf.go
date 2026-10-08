// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package reflect

import (
	"internal/abi"
	"internal/unsafeheader"
	"strings"
	_ "unsafe"
)

//go:linkname isolateCheckProtoValueType runtime.isolateCheckProtoValueType
func isolateCheckProtoValueType(*abi.Type)

// Only the compiler's pinned proto.Clone hook calls this. All reads and
// allocations retain the caller's owner. Provenance comes from the default
// generated-type registry, not a ProtoReflect callback or a package name.
// Coder caches, size caches, and MessageState are deliberately reconstructed
// lazily by protobuf; unknown wire bytes are part of the value and are copied.
//
//go:linkname isolateCloneProto runtime.isolateCloneProto
func isolateCloneProto(value any) any {
	v := ValueOf(value)
	isolateCheckProtoValueType(v.typ())
	return isolateCloneProtoValue(v).Interface()
}

func isolateCloneProtoValue(v Value) Value {
	// This trusted helper may copy protobuf's private unknownFields bytes. It
	// never exposes a reflective value or grants private field access to callers.
	v.flag &^= flagRO
	switch v.Kind() {
	case Pointer:
		if v.Type().Elem().Kind() != Struct {
			if v.IsNil() {
				return Zero(v.Type())
			}
			isolateCheckHeapAccess(v.pointer(), v.Type().Elem().Size(), false)
			out := New(v.Type().Elem())
			out.Elem().Set(isolateCloneProtoValue(v.Elem()))
			return out
		}
		isolateCheckProtoValueType(v.typ())
		if v.IsNil() {
			return Zero(v.Type())
		}
		isolateCheckHeapAccess(v.pointer(), v.Type().Elem().Size(), false)
		out := New(v.Type().Elem())
		source, target := v.Elem(), out.Elem()
		typ := source.Type()
		for i := range typ.NumField() {
			field := typ.Field(i)
			if field.PkgPath != "" && field.Name != "unknownFields" {
				if field.Name == "extensionFields" && !source.Field(i).IsZero() {
					panic("isolate: protobuf extensions are outside the audited clone path")
				}
				continue
			}
			dst := target.Field(i)
			dst.flag &^= flagRO
			value := source.Field(i)
			// Implicit proto3 scalar/bytes presence is determined by the
			// nonzero value. Explicit oneof and proto2 presence is retained.
			if strings.Contains(string(field.Tag), "proto3") && !strings.Contains(string(field.Tag), "oneof") && (value.IsZero() || value.Kind() == Slice && value.Len() == 0) {
				continue
			}
			dst.Set(isolateCloneProtoValue(value))
		}
		return out
	case Interface:
		out := New(v.Type()).Elem()
		if !v.IsNil() {
			out.Set(isolateCloneProtoValue(v.Elem()))
		}
		return out
	case Slice:
		if v.IsNil() || v.Len() == 0 && v.Type().Elem().Kind() != Uint8 {
			return Zero(v.Type())
		}
		isolateCheckHeapAccess((*unsafeheader.Slice)(v.ptr).Data, uintptr(v.Len())*v.Type().Elem().Size(), false)
		out := MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(isolateCloneProtoElement(v.Index(i)))
		}
		return out
	case Map:
		if v.Len() == 0 {
			return Zero(v.Type())
		}
		out := MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), isolateCloneProtoElement(iter.Value()))
		}
		return out
	case Bool, Int, Int32, Int64, Uint8, Uint32, Uint64, Float32, Float64, String:
		return v
	default:
		panic("isolate: unsupported generated protobuf clone field")
	}
}

// Protobuf repeated/map message values normalize nil messages to empty
// messages; byte elements likewise normalize nil to present empty bytes.
func isolateCloneProtoElement(v Value) Value {
	if v.Kind() == Pointer && v.IsNil() {
		isolateCheckProtoValueType(v.typ())
		return New(v.Type().Elem())
	}
	if v.Kind() == Slice && v.Type().Elem().Kind() == Uint8 && v.Len() == 0 {
		return MakeSlice(v.Type(), 0, 0)
	}
	return isolateCloneProtoValue(v)
}
