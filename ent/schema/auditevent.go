package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type AuditEvent struct{ ent.Schema }

func (AuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("package_version_id"),
		field.String("severity"),
		field.String("code"),
		field.String("detail"),
		field.Time("created_at").Immutable(),
	}
}
