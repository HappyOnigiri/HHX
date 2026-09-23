// Package registry は hhx が持つ hook の一覧である。
// install が書くエントリと `hhx hook <name>` の振り分けは、どちらもこの一覧から作る。
package registry

import (
	"github.com/HappyOnigiri/hhx/internal/hookrt"
	"github.com/HappyOnigiri/hhx/internal/hooks/dangerousrmguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/discardguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/exitplansubagentguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/forbiddentermguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/githookspathguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/idlewaitguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/irreversibleguard"
	"github.com/HappyOnigiri/hhx/internal/hooks/prmergeguard"
)

// definitions は hook を設定ファイルへ書く順に並べる。
// 同じ CLI・イベント・matcher のエントリは 1 つのグループにまとまり、グループ内の順序もこの順になる。
// 移行元の Python 実装を登録していた順に合わせ、移植した hook はその位置へ差し込む。
var definitions = []hookrt.Definition{
	prmergeguard.Definition(),
	discardguard.Definition(),
	githookspathguard.Definition(),
	irreversibleguard.Definition(),
	dangerousrmguard.Definition(),
	forbiddentermguard.Definition(),
	idlewaitguard.Definition(),
	exitplansubagentguard.Definition(),
}

// All は登録済みの hook をすべて返す。
func All() []hookrt.Definition {
	return definitions
}

// Lookup は name の hook を返す。未知の名前なら nil を返す。
func Lookup(name string) *hookrt.Definition {
	for index := range definitions {
		if definitions[index].Name == name {
			return &definitions[index]
		}
	}
	return nil
}

// Known は name が登録済みの hook かを返す。
func Known(name string) bool {
	return Lookup(name) != nil
}
