package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Job struct{ ent.Schema }

func (Job) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("package_version_id"),
		field.String("kind").Default("build"),
		field.Enum("status").Values("pending", "running", "built", "published", "failed").Default("pending"),
		field.Time("eligible_at"),
		field.Time("claimed_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
		field.Int("attempts").Default(0),
		field.String("log_path").Optional(),
		field.String("error").Optional(),
		field.Time("created_at").Immutable(),
	}
}

func (Job) Indexes() []ent.Index { return []ent.Index{index.Fields("status", "eligible_at")} }
