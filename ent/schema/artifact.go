package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Artifact struct{ ent.Schema }

func (Artifact) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("job_id"),
		field.String("filename"),
		field.String("sha256"),
		field.Int64("size_bytes"),
		field.Time("published_at").Optional().Nillable(),
	}
}

func (Artifact) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_id", "filename").Unique()}
}
