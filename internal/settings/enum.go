package settings

import (
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// EnumName is how configuration spells e: its contract name less the enum's prefix, lowercased, so DEVICE_TYPE_DLNA is dlna.
func EnumName[E protoreflect.Enum](e E) string {
	v := e.Descriptor().Values().ByNumber(e.Number())
	if v == nil || v.Number() == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(string(v.Name()), prefix(e.Descriptor())))
}

// ParseEnum is the value configuration spells as name, false for none and for the unspecified one.
func ParseEnum[E interface {
	~int32
	protoreflect.Enum
}](name string) (E, bool) {
	var zero E
	v := zero.Descriptor().Values().ByName(protoreflect.Name(prefix(zero.Descriptor()) + strings.ToUpper(name)))
	if v == nil || v.Number() == 0 {
		return zero, false
	}
	return E(v.Number()), true
}

// prefix is what an enum's value names add, read off its unspecified value.
func prefix(d protoreflect.EnumDescriptor) string {
	return strings.TrimSuffix(string(d.Values().ByNumber(0).Name()), "UNSPECIFIED")
}
