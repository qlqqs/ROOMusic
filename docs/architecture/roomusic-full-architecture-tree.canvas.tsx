import {
  Card,
  CardBody,
  CardHeader,
  Code,
  H1,
  H2,
  H3,
  Pill,
  Row,
  Stack,
  Text,
  useHostTheme,
} from "cursor/canvas";

interface ArchitectureLayer {
  title: string;
  path: string;
  modules: string[];
  description: string;
  color: string;
}

const layers: ArchitectureLayer[] = [
  {
    title: "Backend application",
    path: "backend/",
    modules: ["app", "platform", "identity", "library", "catalog", "search", "operations", "agent"],
    description: "单一 Go 进程；按能力划分模块，PostgreSQL 是业务权威。",
    color: "primary",
  },
  {
    title: "Frontend application",
    path: "frontend/",
    modules: ["app", "auth", "library", "catalog", "search", "operations", "agent", "shared"],
    description: "React/TypeScript 同源 Web UI；feature 拥有页面、状态和 REST decoder。",
    color: "green",
  },
  {
    title: "Persistence and runtime",
    path: "migrations/ · deploy/ · data/",
    modules: ["PostgreSQL schema", "managed assets", "optional Redis", "optional Meilisearch", "observability"],
    description: "核心数据与可替换基础设施分开；未来服务不能成为 Core 0 启动前提。",
    color: "cyan",
  },
  {
    title: "Extension boundary",
    path: "extensions/ · plugins/",
    modules: ["capability contracts", "plugin host", "parser providers", "metadata providers", "execution providers"],
    description: "完整架构中的扩展面；只有出现真实需求后才实现，不应反向污染核心模块。",
    color: "purple",
  },
];

const fullTree = `ROOMusic/
├── backend/                             # Go modular monolith
│   ├── cmd/                             # executable entrypoints
│   ├── internal/
│   │   ├── app/                          # composition root and lifecycle
│   │   ├── platform/                     # config, HTTP, database, observability
│   │   ├── identity/                     # setup, users, sessions, access
│   │   ├── library/                      # roots, scanner, parsers, diagnostics
│   │   ├── catalog/                      # Release Graph and provenance
│   │   ├── search/                       # query and rebuildable projections
│   │   ├── operations/                   # changes, journal, revision, recovery
│   │   └── agent/                        # modes, plans, review, tool authority
│   ├── migrations/                       # backend-owned schema history
│   └── testdata/                         # backend fixtures
│
├── frontend/                            # React/TypeScript application
│   ├── src/
│   │   ├── app/                          # routes, providers, shell
│   │   ├── features/
│   │   │   ├── auth/                     # setup, login, session
│   │   │   ├── library/                  # roots, scans, diagnostics
│   │   │   ├── catalog/                  # releases, tracks, artwork views
│   │   │   ├── search/                   # query, filters, results
│   │   │   ├── operations/               # status, history, recovery
│   │   │   └── agent/                    # assistant, steward, operator UI
│   │   ├── shared/                       # API transport, UI, neutral mechanics
│   │   └── test/                         # frontend test setup
│   └── public/                           # static public assets
│
├── deploy/                               # compose, containers, production setup
├── extensions/                           # typed capability contracts
├── plugins/                              # future plugin implementations
├── docs/                                 # architecture, API, ADR, security docs
├── scripts/                              # development, database, verification
├── compose.yaml
├── .env.example
├── Makefile
└── README.md`;

const dependencyTree = `Web / CLI / Automation
          ↓
Versioned REST + session + request_id
          ↓
Capability application services
          ↓
Domain rules + consumer-owned ports
          ↑
PostgreSQL / filesystem / provider adapters

Future extension path:
Capability contract → registry → plugin host → provider plugin
                         (never a Core 0 service locator)`;

function resolveColor(theme: ReturnType<typeof useHostTheme>, color: string): string {
  if (color === "green") return theme.category.green;
  if (color === "cyan") return theme.category.cyan;
  if (color === "purple") return theme.category.purple;
  return theme.accent.primary;
}

export default function RoomusicFullArchitectureTree() {
  const theme = useHostTheme();

  return (
    <Stack gap={22} style={{ padding: 24, maxWidth: 1240, margin: "0 auto" }}>
      <Stack gap={8}>
        <Row gap={8} align="center" wrap>
          <Pill active>完整架构</Pill>
          <Pill>Core 0 是子集</Pill>
          <Pill>能力边界</Pill>
        </Row>
        <H1>ROOMusic 完整架构文件树</H1>
        <Text tone="secondary" style={{ maxWidth: 980 }}>
          完整架构保留未来 Search、Operations、Agent、Artwork 和 Plugin 扩展的位置，但不等于现在全部实现。目录树表达边界和依赖方向；Trellis task 决定每个阶段实际创建哪些目录。
        </Text>
      </Stack>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))", gap: 14 }}>
        {layers.map((layer) => (
          <Card key={layer.title}>
            <CardHeader trailing={<span style={{ color: resolveColor(theme, layer.color) }}>layer</span>}>{layer.title}</CardHeader>
            <CardBody>
              <Code>{layer.path}</Code>
              <Text size="small" style={{ marginTop: 8 }}>{layer.description}</Text>
              <Text size="small" tone="secondary" style={{ marginTop: 8 }}>{layer.modules.join(" · ")}</Text>
            </CardBody>
          </Card>
        ))}
      </div>

      <Stack gap={9}>
        <H2>完整模块树</H2>
        <Card><CardBody><Code>{fullTree}</Code></CardBody></Card>
      </Stack>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 18 }}>
        <Stack gap={9}>
          <H2>依赖骨架</H2>
          <Card><CardBody><Code>{dependencyTree}</Code></CardBody></Card>
        </Stack>
        <Stack gap={9}>
          <H2>能力 ownership</H2>
          <div style={{ display: "grid", gap: 9 }}>
            <div style={{ borderLeft: `4px solid ${theme.category.green}`, paddingLeft: 12 }}><H3>Identity</H3><Text size="small" tone="secondary">管理员初始化、session、角色与访问 authority。</Text></div>
            <div style={{ borderLeft: `4px solid ${theme.category.orange}`, paddingLeft: 12 }}><H3>Library</H3><Text size="small" tone="secondary">目录安全、只读扫描、解析观察、诊断和 missing reconciliation。</Text></div>
            <div style={{ borderLeft: `4px solid ${theme.accent.primary}`, paddingLeft: 12 }}><H3>Catalog</H3><Text size="small" tone="secondary">Release Graph、来源、字段 observation 和浏览读模型。</Text></div>
            <div style={{ borderLeft: `4px solid ${theme.category.purple}`, paddingLeft: 12 }}><H3>Operations / Agent</H3><Text size="small" tone="secondary">后续才实现 Change Set、审计、恢复、三模式 Agent 和工具权威。</Text></div>
          </div>
        </Stack>
      </div>

      <Stack gap={8}>
        <H2>Core 0 与完整架构的关系</H2>
        <Text>Core 0 实际启用：app、platform、identity、library、catalog，以及前端 auth、library、catalog。</Text>
        <Text>第二阶段再按独立任务启用：search、artwork、private multi-user、operations governance 和 format/CUE expansion。</Text>
        <Text>最后才启用：Agent runtime、Capability Registry、Plugin Host、provider plugins、execution plugins，以及 Redis/Meilisearch 等可选基础设施。</Text>
      </Stack>

      <Text size="small" tone="tertiary">依据：架构 Canvas、Trellis modular-design、backend/frontend directory-structure、product-goals 与当前 Core 0 PRD。</Text>
    </Stack>
  );
}
