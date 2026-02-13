package translations

//go:generate gotext -srclang=en-US update -out=catalog.go -lang=en-US,de-DE,fr-FR,zh-CN,zh-TW,it-IT,ja-JP,ko-KR,pt-BR,ru-RU,es-ES github.com/sgrankin/go-sqlcmd/cmd/sqlcmd github.com/sgrankin/go-sqlcmd/internal/legacy github.com/sgrankin/go-sqlcmd/pkg/sqlcmd
