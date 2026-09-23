// Package version は build 時に埋め込む版情報を提供する。
package version

// Version は linker flag で埋め込む build 版である。
var Version = "undefined"

// BuildMeta は build 版に付加するメタデータである。
var BuildMeta = "dev"

// String は build 版とメタデータを表示用に結合する。
func String() string {
	if BuildMeta == "" {
		return Version
	}
	return Version + "-" + BuildMeta
}
