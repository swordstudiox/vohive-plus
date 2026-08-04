module github.com/swordstudiox/vohive-plus

go 1.26.4

replace (
	github.com/iniwex5/netlink => ./third_party/netlink
	github.com/iniwex5/qqbot => ./third_party/qqbot
	github.com/iniwex5/quectel-qmi-go => ./third_party/quectel-qmi-go
	github.com/iniwex5/vowifi-go => ./third_party/vowifi-go
)

require (
	github.com/damonto/euicc-go v1.1.3-0.20260628013808-8d873a2dfc98
	github.com/gin-gonic/gin v1.11.0
	github.com/glebarez/sqlite v1.11.0
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
	github.com/iniwex5/netlink v1.3.3
	github.com/iniwex5/qqbot v1.0.1
	github.com/iniwex5/quectel-qmi-go v0.6.0
	github.com/iniwex5/vowifi-go v1.1.2
	github.com/larksuite/oapi-sdk-go/v3 v3.5.3
	github.com/lestrrat-go/file-rotatelogs v2.4.0+incompatible
	github.com/miekg/dns v1.1.67
	github.com/spf13/viper v1.21.0
	github.com/stretchr/testify v1.11.1
	github.com/things-go/go-socks5 v0.1.1
	github.com/warthog618/sms v0.3.0
	go.bug.st/serial v1.6.4
	go.uber.org/zap v1.27.1
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/crypto v0.49.0
	golang.org/x/sync v0.20.0
	golang.org/x/sys v0.46.0
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/gorm v1.30.5
)

require (
	github.com/bytedance/sonic v1.14.0 // indirect
	github.com/bytedance/sonic/loader v0.3.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/creack/goselect v0.1.2 // indirect
	github.com/damonto/uicc-go v0.0.0-20260622025011-5cf36d6c8626 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/emiago/sipgo v1.4.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.8 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.27.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/goccy/go-yaml v1.18.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/pprof v0.0.0-20231101202521-4ca4178f5c7a // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/lestrrat-go/strftime v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.16 // indirect
	github.com/pion/rtp v1.10.2 // indirect
	github.com/pion/srtp/v3 v3.0.12 // indirect
	github.com/pion/transport/v4 v4.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.57.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rogpeppe/go-internal v1.13.1 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.0 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/arch v0.20.0 // indirect
	golang.org/x/mod v0.34.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.22.5 // indirect
	modernc.org/mathutil v1.5.0 // indirect
	modernc.org/memory v1.5.0 // indirect
	modernc.org/sqlite v1.23.1 // indirect
)
