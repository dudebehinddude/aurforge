package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ManagedPackage struct{ ent.Schema }

func (ManagedPackage) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique(),
		field.Enum("source_kind").Values("aur", "local"),
		field.String("source_ref"),
		field.String("source_path"),
		field.Bool("enabled").Default(true),
		field.Time("created_at").Immutable(),
		field.Time("updated_at").UpdateDefault(func() time.Time { return time.Now() }),
	}
}

func (ManagedPackage) Indexes() []ent.Index { return []ent.Index{index.Fields("name")} }
