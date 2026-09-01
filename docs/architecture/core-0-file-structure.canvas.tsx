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

interface Boundary {
  title: string;
  purpose: string;
  path: string;
  rule: string;
  tone: "primary" | "green" | "orange" | "purple";
}

const boundaries: Boundary[] = [
  {
    title: "Composition root",
    purpose: "唯一负责组装依赖、挂载路由与生命周期",
    path: "backend/internal/app/",
    rule: "可以导入具体 adapter；业务模块不能反向依赖 app",
    tone: "primary",
  },
  {
    title: "Platform mechanisms",
    purpose: "配置、HTTP、PostgreSQL、迁移、日志与 request_id",
    path: "backend/internal/platform/",
    rule: "只提供机制，不拥有 identity、scan 或 catalog 业务规则",
    tone: "purple",
  },
  {
    title: "Identity capability",
    purpose: "一次性 setup、管理员、密码、opaque session 与撤销",
    path: "backend/internal/identity/",
    rule: "拥有认证与角色 authority；不拥有前端导航或目录策略",
    tone: "green",
  },
  {
    title: "Library capability",
    purpose: "allowed root、路径安全、只读遍历、解析协调、scan 与诊断",
    path: "backend/internal/library/",
    rule: "通过 catalog 的窄写入合同提交 observation，不写 catalog 私表",
    tone: "orange",
  },
  {
    title: "Catalog capability",
    purpose: "ReleaseGroup → Release → Medium → Track 与 provenance",
    path: "backend/internal/catalog/",
    rule: "拥有图谱不变量；不执行 filesystem walk，不解析 HTTP",
    tone: "primary",
  },
];

const backendTree = `backend/
├── cmd/roomusic/main.go
├── internal/
│   ├── app/                         # composition root
│   ├── platform/
│   │   ├── config/
│   │   ├── httpserver/
│   │   ├── observability/
│   │   └── postgres/
│   ├── identity/
│   │   ├── domain/ application/ ports/
│   │   ├── adapters/postgres/
│   │   └── transport/httpapi/
│   ├── library/
│   │   ├── domain/ application/ ports/
│   │   ├── adapters/filesystem/ adapters/postgres/
│   │   ├── parser/
│   │   └── transport/httpapi/
│   └── catalog/
│       ├── domain/ application/ ports/
│       ├── adapters/postgres/
│       └── transport/httpapi/
├── migrations/
└── testdata/`;

const frontendTree = `frontend/
├── src/
│   ├── app/
│   │   ├── routes/ providers/ shell/
│   │   └── main.tsx
│   ├── features/
│   │   ├── auth/                    # setup/login/logout/session
│   │   ├── library/                 # roots, scans, diagnostics
│   │   └── catalog/                 # releases, media, tracks
│   ├── shared/
│   │   ├── api/                     # transport + common errors
│   │   ├── ui/                      # policy-free primitives
│   │   └── lib/                     # domain-neutral mechanics
│   ├── assets/
│   └── test/setup/
└── public/`;

function getToneColor(theme: ReturnType<typeof useHostTheme>, tone: Boundary["tone"]): string {
  if (tone === "green") return theme.category.green;
  if (tone === "orange") return theme.category.orange;
  if (tone === "purple") return theme.category.purple;
  return theme.accent.primary;
}

export default function CoreZeroFileStructure() {
  const theme = useHostTheme();

  return (
    <Stack gap={22} style={{ padding: 24, maxWidth: 1220, margin: "0 auto" }}>
      <Stack gap={8}>
        <Row gap={8} align="center" wrap>
          <Pill active>Core 0</Pill>
          <Pill>模块化单体</Pill>
          <Pill>首个可浏览纵向切片</Pill>
        </Row>
        <H1>按能力边界组织，不按技术类型堆叠</H1>
        <Text tone="secondary" style={{ maxWidth: 980 }}>
          最合适的结构是 backend 与 frontend 分层、每层内部按 capability 垂直组织；domain、application、ports、adapters、transport 只作为能力内部的职责边界。这样既符合架构图的模块化单体，也符合 Trellis spec 对 ownership、依赖方向和可验证性的要求。
        </Text>
      </Stack>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", gap: 14 }}>
        {boundaries.map((boundary) => (
          <Card key={boundary.title}>
            <CardHeader trailing={<span style={{ color: getToneColor(theme, boundary.tone) }}>owner</span>}>
              {boundary.title}
            </CardHeader>
            <CardBody>
              <Code>{boundary.path}</Code>
              <Text size="small" style={{ marginTop: 9 }}>{boundary.purpose}</Text>
              <Text size="small" tone="secondary" style={{ marginTop: 7 }}>{boundary.rule}</Text>
            </CardBody>
          </Card>
        ))}
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 18 }}>
        <Stack gap={9}>
          <H2>推荐后端树</H2>
          <Card><CardBody><Code>{backendTree}</Code></CardBody></Card>
        </Stack>
        <Stack gap={9}>
          <H2>推荐前端树</H2>
          <Card><CardBody><Code>{frontendTree}</Code></CardBody></Card>
        </Stack>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "1.1fr 1fr 1fr", gap: 14 }}>
        <div style={{ borderTop: `4px solid ${theme.accent.primary}`, paddingTop: 12 }}>
          <H3>后端依赖方向</H3>
          <Code>transport → application → domain + ports
adapters → ports
app → concrete adapters</Code>
        </div>
        <div style={{ borderTop: `4px solid ${theme.category.green}`, paddingTop: 12 }}>
          <H3>前端数据方向</H3>
          <Code>route → feature component
→ feature hook → feature API/decoder
→ shared HTTP → /api/v1</Code>
        </div>
        <div style={{ borderTop: `4px solid ${theme.category.orange}`, paddingTop: 12 }}>
          <H3>首切片只创建</H3>
          <Text size="small" tone="secondary">auth、library、catalog、platform 和 app。暂不创建 search、artwork、operations、plugins、Agent、Redis/Meilisearch client。</Text>
        </div>
      </div>

      <Stack gap={8}>
        <H2>落地规则</H2>
        <Text>1. 每个能力拥有自己的 policy、写入用例、ports、adapter 与测试。</Text>
        <Text>2. 不创建全局 controllers、models、repositories、services 或 utils 目录。</Text>
        <Text>3. 跨能力只调用公开的 typed application contract；禁止读取别的能力私有表和 row 类型。</Text>
        <Text>4. 先建立真实使用到的目录，避免为未来插件和操作框架预留空壳。</Text>
        <Text>5. 新选定的 Go/React 工具链与质量命令，在 scaffold 完成后回写对应 Trellis spec。</Text>
      </Stack>

      <Text size="small" tone="tertiary">依据：roomusic-modular-plugin-architecture.canvas.tsx、backend/frontend directory-structure.md、modular-design.md、cross-layer-thinking-guide.md 与首个浏览切片 design.md。</Text>
    </Stack>
  );
}
