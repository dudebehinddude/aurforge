package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PackageVersion struct{ ent.Schema }

func (PackageVersion) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("package_id"),
		field.String("revision"),
		field.String("source_digest"),
		field.String("pkgver"),
		field.String("pkgrel"),
		field.JSON("metadata", map[string]any{}),
		field.Time("first_seen_at").Immutable(),
	}
}

func (PackageVersion) Indexes() []ent.Index {
	return []ent.Index{index.Fields("package_id", "revision").Unique()}
}
