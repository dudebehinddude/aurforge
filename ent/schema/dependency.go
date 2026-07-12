package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Dependency struct{ ent.Schema }

func (Dependency) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("package_version_id"),
		field.String("dependency_name"),
		field.Enum("dependency_kind").Values("repo", "aur"),
	}
}

func (Dependency) Indexes() []ent.Index {
	return []ent.Index{index.Fields("package_version_id", "dependency_name").Unique()}
}
