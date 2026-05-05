module github.com/cozystack/talm

go 1.26.2

// Kubernetes dependencies sharing the same version.
require (
	k8s.io/api v0.35.3
	k8s.io/apimachinery v0.35.3
	k8s.io/client-go v0.35.3
	k8s.io/component-base v0.35.3 // indirect
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.21.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azcertificates v1.4.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys v1.4.0 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.14 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/kms v1.50.4 // indirect
	github.com/aws/smithy-go v1.24.3 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/containernetworking/cni v1.3.0 // indirect
	github.com/cosi-project/runtime v1.14.1
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/cli v29.4.0+incompatible // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/foxboron/go-uefi v0.0.0-20251010190908-d29549a44f29 // indirect
	github.com/gdamore/tcell/v2 v2.13.8 // indirect
	github.com/gertd/go-pluralize v0.2.1 // indirect
	github.com/gizak/termui/v3 v3.1.0 // indirect
	github.com/google/cel-go v0.28.0 // indirect
	github.com/google/go-containerregistry v0.21.4 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gopacket/gopacket v1.5.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1
	github.com/jsimonetti/rtnetlink/v2 v2.2.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/mattn/go-isatty v0.0.21 // indirect
	github.com/mdlayher/ethtool v0.6.0 // indirect
	github.com/mdlayher/genetlink v1.4.0 // indirect
	github.com/mdlayher/netlink v1.10.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/rivo/tview v0.42.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/ryanuber/columnize v2.1.2+incompatible // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/siderolabs/crypto v0.6.5
	github.com/siderolabs/gen v0.8.6 // indirect
	github.com/siderolabs/go-api-signature v0.3.12 // indirect
	github.com/siderolabs/go-circular v0.2.3 // indirect
	github.com/siderolabs/go-kubeconfig v0.1.2 // indirect
	github.com/siderolabs/go-kubernetes v0.2.30 // indirect
	github.com/siderolabs/go-pointer v1.0.1 // indirect
	github.com/siderolabs/go-procfs v0.1.2 // indirect
	github.com/siderolabs/go-retry v0.3.3 // indirect
	github.com/siderolabs/go-talos-support v0.2.0 // indirect
	github.com/siderolabs/net v0.4.0 // indirect
	github.com/siderolabs/proto-codec v0.1.4 // indirect
	github.com/siderolabs/talos/pkg/machinery v1.13.0-beta.1
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	github.com/stretchr/testify v1.11.1
	go.etcd.io/etcd/api/v3 v3.6.10 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.6.10 // indirect
	go.etcd.io/etcd/client/v3 v3.6.10 // indirect
	go.etcd.io/etcd/etcdutl/v3 v3.6.10 // indirect
	go.uber.org/zap v1.27.1 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0
	golang.org/x/term v0.42.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20241231184526-a9ab2273dd10 // indirect
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/klog/v2 v2.140.0 // indirect
	sigs.k8s.io/hydrophone v0.7.0 // indirect
	sigs.k8s.io/yaml v1.6.0
)

require (
	filippo.io/age v1.3.1
	github.com/BurntSushi/toml v1.6.0
	github.com/Masterminds/sprig/v3 v3.3.0
	github.com/cockroachdb/errors v1.13.0
	github.com/gobwas/glob v0.2.3
	github.com/pkg/errors v0.9.1
	github.com/siderolabs/talos v1.12.6
	helm.sh/helm/v3 v3.20.2
)

require (
	c2sp.org/CCTV/age v0.0.0-20260405221107-d5c22d3131c5 // indirect
	cel.dev/expr v0.25.1 // indirect
	dario.cat/mergo v1.0.2 // indirect
	filippo.io/hpke v0.4.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azidentity/cache v0.4.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/internal v1.2.0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.7.1 // indirect
	github.com/MakeNowJust/heredoc v1.0.0 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/ProtonMail/go-crypto v1.4.1 // indirect
	github.com/ProtonMail/go-mime v0.0.0-20230322103455-7d82a3887f2f // indirect
	github.com/ProtonMail/gopenpgp/v2 v2.10.0 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/armon/circbuf v0.0.0-20190214190532-5111143e8da2 // indirect
	github.com/aws/aws-sdk-go-v2 v1.41.5 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.14 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.21 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.10 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/brianvoe/gofakeit/v7 v7.14.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chai2010/gettext-go v1.0.3 // indirect
	github.com/cilium/ebpf v0.21.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/cockroachdb/logtags v0.0.0-20230118201751-21c54148d20b // indirect
	github.com/cockroachdb/redact v1.1.5 // indirect
	github.com/containerd/containerd/v2 v2.2.2 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/go-cni v1.1.13 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/stargz-snapshotter/estargz v0.18.2 // indirect
	github.com/coreos/go-semver v0.3.1 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/cyphar/filepath-securejoin v0.6.1 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/docker/docker-credential-helpers v0.9.5 // indirect
	github.com/emicklei/dot v1.11.0 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/evanphx/json-patch v5.9.11+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/exponent-io/jsonpath v0.0.0-20210407135951-1de76d718b3f // indirect
	github.com/fatih/camelcase v1.0.0 // indirect
	github.com/fluxcd/cli-utils v1.0.0 // indirect
	github.com/fluxcd/pkg/ssa v0.70.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.1 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/getsentry/sentry-go v0.46.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.22.5 // indirect
	github.com/go-openapi/jsonreference v0.21.5 // indirect
	github.com/go-openapi/swag v0.25.5 // indirect
	github.com/go-openapi/swag/cmdutils v0.25.5 // indirect
	github.com/go-openapi/swag/conv v0.25.5 // indirect
	github.com/go-openapi/swag/fileutils v0.25.5 // indirect
	github.com/go-openapi/swag/jsonname v0.25.5 // indirect
	github.com/go-openapi/swag/jsonutils v0.25.5 // indirect
	github.com/go-openapi/swag/loading v0.25.5 // indirect
	github.com/go-openapi/swag/mangling v0.25.5 // indirect
	github.com/go-openapi/swag/netutils v0.25.5 // indirect
	github.com/go-openapi/swag/stringutils v0.25.5 // indirect
	github.com/go-openapi/swag/typeutils v0.25.5 // indirect
	github.com/go-openapi/swag/yamlutils v0.25.5 // indirect
	github.com/go-openapi/testify/enable/yaml/v2 v2.4.2 // indirect
	github.com/go-openapi/testify/v2 v2.4.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-tpm-tools v0.4.9-0.20260325175049-22911efba9e5 // indirect
	github.com/google/pprof v0.0.0-20260402051712-545e8a4df936 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/gosuri/uilive v0.0.4 // indirect
	github.com/gosuri/uiprogress v0.0.1 // indirect
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hexops/gotextdiff v1.0.3 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/lmittmann/tint v1.1.3 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/mdlayher/socket v0.6.0 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/moby/moby/api v1.54.1 // indirect
	github.com/moby/spdystream v0.5.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/nsf/termbox-go v1.1.1 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/petermattis/goid v0.0.0-20260330135022-df67b199bc81 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/xattr v0.4.12 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20250313105119-ba97887b0a25 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/sasha-s/go-deadlock v0.3.9 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/siderolabs/go-cmd v0.2.0 // indirect
	github.com/siderolabs/protoenc v0.2.4 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spyzhov/ajson v0.9.6 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/vbatts/tar-split v0.12.2 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xiang90/probing v0.0.0-20221125231312-a49e3df8f510 // indirect
	github.com/xlab/treeprint v1.2.0 // indirect
	go.etcd.io/bbolt v1.4.3 // indirect
	go.etcd.io/etcd/pkg/v3 v3.6.10 // indirect
	go.etcd.io/etcd/server/v3 v3.6.10 // indirect
	go.etcd.io/raft/v3 v3.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.68.0 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.43.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.4 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260406210006-6f92a3bedf2d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260406210006-6f92a3bedf2d // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/apiextensions-apiserver v0.35.3 // indirect
	k8s.io/cli-runtime v0.35.3 // indirect
	k8s.io/component-helpers v0.35.3 // indirect
	k8s.io/kube-openapi v0.0.0-20260330154417-16be699c7b31 // indirect
	k8s.io/kubectl v0.35.3 // indirect
	k8s.io/utils v0.0.0-20260319190234-28399d86e0b5 // indirect
	sigs.k8s.io/cli-utils v0.37.3-0.20250918194211-77c836a69463 // indirect
	sigs.k8s.io/controller-runtime v0.23.3 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/kustomize/api v0.21.1 // indirect
	sigs.k8s.io/kustomize/kyaml v0.21.1 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.2 // indirect
)

exclude github.com/containerd/containerd v1.7.0

// Replace Sidero Talos with Cozystack Talos fork to include the following fix:
// - https://github.com/siderolabs/talos/pull/12652 (--skip-verify)
//   Upstream declined this flag citing a risk of leaking sensitive data to
//   a malicious actor impersonating the Talos API, so the fork is required
//   for as long as we need --skip-verify.
replace (
	github.com/siderolabs/talos => github.com/cozystack/talos v0.0.0-20260126122716-d18a185e3680
	github.com/siderolabs/talos/pkg/machinery => github.com/cozystack/talos/pkg/machinery v0.0.0-20260126122716-d18a185e3680
)
