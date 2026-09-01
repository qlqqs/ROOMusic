import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Code,
  Divider,
  H1,
  H2,
  H3,
  Pill,
  Row,
  Stack,
  Text,
  useHostTheme,
  useState,
} from "cursor/canvas";

type ArchitectureFocus = "all" | "kernel" | "modules" | "plugins" | "flow";
type NodeKind = "entry" | "kernel" | "module" | "host" | "plugin" | "data";

interface ArchitectureNode {
  id: string;
  title: string;
  lines: string[];
  kind: NodeKind;
  x: number;
  y: number;
  width: number;
  height: number;
  dashed?: boolean;
}

interface ArchitectureEdge {
  from: string;
  to: string;
  label: string;
  style: "core" | "extension" | "data";
}

const architectureNodes: ArchitectureNode[] = [
  {
    id: "web-ui",
    title: "Web UI",
    lines: ["Library / Search / Operations", "Agent and admin surfaces"],
    kind: "entry",
    x: 32,
    y: 68,
    width: 224,
    height: 88,
  },
  {
    id: "api-entry",
    title: "API Entry",
    lines: ["Versioned REST API", "Session + request_id"],
    kind: "entry",
    x: 280,
    y: 68,
    width: 224,
    height: 88,
  },
  {
    id: "agent-console",
    title: "Admin Console / Future Agent UI",
    lines: ["Core 0 admin operations", "Agent modes are contract-only"],
    kind: "entry",
    x: 528,
    y: 68,
    width: 248,
    height: 88,
  },
  {
    id: "cli-automation",
    title: "CLI / Automation",
    lines: ["Same contracts as Web UI", "No authority bypass"],
    kind: "entry",
    x: 800,
    y: 68,
    width: 224,
    height: 88,
  },
  {
    id: "domain-kernel",
    title: "Domain Kernel",
    lines: ["ReleaseGroup → Release → Medium → Track", "Identity, provenance and revisions"],
    kind: "kernel",
    x: 32,
    y: 234,
    width: 224,
    height: 96,
  },
  {
    id: "security-kernel",
    title: "Security + Mode Kernel",
    lines: ["User, session and capabilities", "Assistant / Steward / Operator rules"],
    kind: "kernel",
    x: 280,
    y: 234,
    width: 224,
    height: 96,
  },
  {
    id: "change-kernel",
    title: "Change Management Kernel",
    lines: ["Change Set + Operation Journal", "Checkpoint, idempotency and rollback"],
    kind: "kernel",
    x: 528,
    y: 234,
    width: 248,
    height: 96,
  },
  {
    id: "tool-authority",
    title: "Tool Authority Kernel",
    lines: ["Risk floor and allowlisted tools", "Validation, transaction and execution"],
    kind: "kernel",
    x: 800,
    y: 234,
    width: 224,
    height: 96,
  },
  {
    id: "persistence-kernel",
    title: "Persistence Kernel",
    lines: ["Core schema and migrations", "Transactional authority writes"],
    kind: "kernel",
    x: 404,
    y: 354,
    width: 248,
    height: 88,
  },
  {
    id: "auth-module",
    title: "Auth Module",
    lines: ["Setup, login and roles", "First-party, compile-time"],
    kind: "module",
    x: 32,
    y: 522,
    width: 184,
    height: 90,
  },
  {
    id: "library-module",
    title: "Library + Scanner",
    lines: ["Built-in FLAC/MP3/OGG/Opus/WAV + CUE", "Read-only parse and reconciliation"],
    kind: "module",
    x: 232,
    y: 522,
    width: 184,
    height: 90,
  },
  {
    id: "graph-module",
    title: "Release Graph",
    lines: ["Catalog read/write model", "Evidence and source projection"],
    kind: "module",
    x: 432,
    y: 522,
    width: 184,
    height: 90,
  },
  {
    id: "search-module",
    title: "Search Module",
    lines: ["Query and projection", "Index is rebuildable"],
    kind: "module",
    x: 632,
    y: 522,
    width: 184,
    height: 90,
  },
  {
    id: "agent-module",
    title: "Agent Orchestrator",
    lines: ["Future mode routing and plans", "Core 0 defines contract only"],
    kind: "module",
    x: 832,
    y: 522,
    width: 192,
    height: 90,
  },
  {
    id: "operation-module",
    title: "Operation Center",
    lines: ["Status, history and recovery", "Shared by UI, CLI and Agent"],
    kind: "module",
    x: 332,
    y: 632,
    width: 392,
    height: 82,
  },
  {
    id: "capability-registry",
    title: "Capability Registry",
    lines: ["Definitions, providers and consumers", "Typed contracts + lifecycle"],
    kind: "host",
    x: 32,
    y: 790,
    width: 304,
    height: 94,
  },
  {
    id: "plugin-host",
    title: "Plugin Host",
    lines: ["Manifest, protocol and health", "Permission grants + unload cleanup"],
    kind: "host",
    x: 360,
    y: 790,
    width: 304,
    height: 94,
  },
  {
    id: "profile-composer",
    title: "Profiles / Bundles",
    lines: ["minimal / standard / agent / full", "Ordered composition, explicit config"],
    kind: "host",
    x: 688,
    y: 790,
    width: 336,
    height: 94,
  },
  {
    id: "parser-plugins",
    title: "Future Media Parser Plugins",
    lines: ["External formats and parser variants", "Return observations only"],
    kind: "plugin",
    x: 32,
    y: 952,
    width: 232,
    height: 92,
    dashed: true,
  },
  {
    id: "provider-plugins",
    title: "Future Provider Plugins",
    lines: ["MusicBrainz, Discogs, Cover Art", "Candidates + provenance only"],
    kind: "plugin",
    x: 280,
    y: 952,
    width: 232,
    height: 92,
    dashed: true,
  },
  {
    id: "agent-plugins",
    title: "Future Agent + Review Plugins",
    lines: ["Model adapter and review provider", "Structured result, no authority"],
    kind: "plugin",
    x: 528,
    y: 952,
    width: 248,
    height: 92,
    dashed: true,
  },
  {
    id: "execution-plugins",
    title: "Future Execution Plugins",
    lines: ["Playback and file/tag operations", "Must implement reversible contract"],
    kind: "plugin",
    x: 792,
    y: 952,
    width: 232,
    height: 92,
    dashed: true,
  },
  {
    id: "postgres",
    title: "PostgreSQL",
    lines: ["Only business authority"],
    kind: "data",
    x: 32,
    y: 1124,
    width: 224,
    height: 70,
  },
  {
    id: "redis",
    title: "Optional Redis (post-Core 0)",
    lines: ["Jobs and retry runtime; not required"],
    kind: "data",
    x: 280,
    y: 1124,
    width: 224,
    height: 70,
  },
  {
    id: "meilisearch",
    title: "Optional Meilisearch (post-Core 0)",
    lines: ["Replaceable search projection; not required"],
    kind: "data",
    x: 528,
    y: 1124,
    width: 248,
    height: 70,
  },
  {
    id: "music-storage",
    title: "Music + Recovery Storage",
    lines: ["Read-only library; no file mutation in Core 0"],
    kind: "data",
    x: 800,
    y: 1124,
    width: 224,
    height: 70,
  },
];

const architectureEdges: ArchitectureEdge[] = [
  { from: "web-ui", to: "domain-kernel", label: "product requests", style: "core" },
  { from: "api-entry", to: "security-kernel", label: "authenticated entry", style: "core" },
  { from: "agent-console", to: "change-kernel", label: "plans / approvals", style: "core" },
  { from: "cli-automation", to: "tool-authority", label: "same authority", style: "core" },
  { from: "domain-kernel", to: "persistence-kernel", label: "validated state", style: "core" },
  { from: "security-kernel", to: "persistence-kernel", label: "principal / policy", style: "core" },
  { from: "change-kernel", to: "persistence-kernel", label: "journal / checkpoint", style: "core" },
  { from: "tool-authority", to: "persistence-kernel", label: "transaction result", style: "core" },
  { from: "auth-module", to: "persistence-kernel", label: "session / user repositories", style: "core" },
  { from: "library-module", to: "persistence-kernel", label: "scan and catalog repositories", style: "core" },
  { from: "graph-module", to: "persistence-kernel", label: "graph repositories", style: "core" },
  { from: "search-module", to: "persistence-kernel", label: "PostgreSQL query", style: "core" },
  { from: "agent-module", to: "persistence-kernel", label: "future authority boundary", style: "core" },
  { from: "operation-module", to: "persistence-kernel", label: "journal / revision repositories", style: "core" },
  { from: "library-module", to: "operation-module", label: "scan operations", style: "core" },
  { from: "agent-module", to: "operation-module", label: "agent operations", style: "core" },
  { from: "operation-module", to: "capability-registry", label: "future capability request", style: "extension" },
  { from: "capability-registry", to: "plugin-host", label: "resolve provider", style: "extension" },
  { from: "profile-composer", to: "plugin-host", label: "compose runtime", style: "extension" },
  { from: "plugin-host", to: "parser-plugins", label: "typed RPC / in-process", style: "extension" },
  { from: "plugin-host", to: "provider-plugins", label: "typed RPC / in-process", style: "extension" },
  { from: "plugin-host", to: "agent-plugins", label: "typed RPC / in-process", style: "extension" },
  { from: "plugin-host", to: "execution-plugins", label: "authority-wrapped call", style: "extension" },
  { from: "persistence-kernel", to: "postgres", label: "transactional authority", style: "data" },
  { from: "operation-module", to: "redis", label: "optional background work", style: "data" },
  { from: "search-module", to: "meilisearch", label: "optional index", style: "data" },
  { from: "execution-plugins", to: "music-storage", label: "scoped operations", style: "data" },
];

function findNode(nodeId: string): ArchitectureNode {
  const node = architectureNodes.find((candidate) => candidate.id === nodeId);
  if (!node) {
    throw new Error(`Unknown architecture node: ${nodeId}`);
  }
  return node;
}

function nodeMatchesFocus(node: ArchitectureNode, focus: ArchitectureFocus): boolean {
  if (focus === "all" || focus === "flow") {
    return true;
  }
  if (focus === "kernel") {
    return node.kind === "kernel" || node.kind === "entry" || node.kind === "data";
  }
  if (focus === "modules") {
    return node.kind === "module" || node.kind === "kernel";
  }
  return node.kind === "host" || node.kind === "plugin";
}

function nodeColor(theme: ReturnType<typeof useHostTheme>, kind: NodeKind): string {
  if (kind === "kernel") return theme.category.red;
  if (kind === "module") return theme.accent.primary;
  if (kind === "host") return theme.category.purple;
  if (kind === "plugin") return theme.category.orange;
  if (kind === "data") return theme.category.cyan;
  return theme.text.tertiary;
}

function edgeOpacity(edge: ArchitectureEdge, focus: ArchitectureFocus): number {
  if (focus === "all" || focus === "flow") return 1;
  if (focus === "plugins") return edge.style === "extension" ? 1 : 0.12;
  if (focus === "kernel") return edge.style === "core" || edge.style === "data" ? 1 : 0.12;
  return edge.style === "core" ? 0.8 : 0.18;
}

function DiagramEdge({ edge, focus }: { edge: ArchitectureEdge; focus: ArchitectureFocus }) {
  const theme = useHostTheme();
  const source = findNode(edge.from);
  const target = findNode(edge.to);
  const sourceX = source.x + source.width / 2;
  const sourceY = source.y + source.height;
  const targetX = target.x + target.width / 2;
  const targetY = target.y;
  const middleY = sourceY + (targetY - sourceY) / 2;
  const stroke = edge.style === "extension" ? theme.category.orange : edge.style === "data" ? theme.category.cyan : theme.stroke.secondary;
  const markerId = edge.style === "extension" ? "extension-arrow" : edge.style === "data" ? "data-arrow" : "core-arrow";

  return (
    <g opacity={edgeOpacity(edge, focus)}>
      <path
        d={`M ${sourceX} ${sourceY} C ${sourceX} ${middleY}, ${targetX} ${middleY}, ${targetX} ${targetY}`}
        fill="none"
        stroke={stroke}
        strokeWidth={edge.style === "core" ? 1.7 : 1.25}
        strokeDasharray={edge.style === "extension" ? "6 4" : undefined}
        markerEnd={`url(#${markerId})`}
      />
      <text
        x={(sourceX + targetX) / 2}
        y={middleY - 5}
        textAnchor="middle"
        fill={theme.text.tertiary}
        fontSize="9.5"
        fontFamily="ui-sans-serif, system-ui, sans-serif"
      >
        {edge.label}
      </text>
    </g>
  );
}

function DiagramNode({ node, focus }: { node: ArchitectureNode; focus: ArchitectureFocus }) {
  const theme = useHostTheme();
  const highlighted = nodeMatchesFocus(node, focus);
  const color = nodeColor(theme, node.kind);

  return (
    <g opacity={highlighted ? 1 : 0.18}>
      <rect
        x={node.x}
        y={node.y}
        width={node.width}
        height={node.height}
        rx="8"
        fill={node.kind === "plugin" ? theme.fill.quaternary : theme.bg.elevated}
        stroke={color}
        strokeWidth="1.4"
        strokeDasharray={node.dashed ? "6 4" : undefined}
      />
      <rect x={node.x} y={node.y} width="5" height={node.height} rx="2" fill={color} />
      <text
        x={node.x + 17}
        y={node.y + 24}
        fill={theme.text.primary}
        fontSize="13"
        fontWeight="600"
        fontFamily="ui-sans-serif, system-ui, sans-serif"
      >
        {node.title}
      </text>
      {node.lines.map((line, lineIndex) => (
        <text
          key={`${node.id}-${line}`}
          x={node.x + 17}
          y={node.y + 48 + lineIndex * 17}
          fill={theme.text.secondary}
          fontSize="10.5"
          fontFamily="ui-sans-serif, system-ui, sans-serif"
        >
          {line}
        </text>
      ))}
    </g>
  );
}

function ArchitectureDiagram({ focus }: { focus: ArchitectureFocus }) {
  const theme = useHostTheme();

  return (
    <svg
      viewBox="0 0 1056 1238"
      role="img"
      aria-label="ROOMusic 模块化核心与插件能力架构图"
      style={{ display: "block", width: "100%", minWidth: 820 }}
    >
      <defs>
        <marker id="core-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
          <path d="M 0 0 L 8 4 L 0 8 z" fill={theme.stroke.secondary} />
        </marker>
        <marker id="extension-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
          <path d="M 0 0 L 8 4 L 0 8 z" fill={theme.category.orange} />
        </marker>
        <marker id="data-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
          <path d="M 0 0 L 8 4 L 0 8 z" fill={theme.category.cyan} />
        </marker>
      </defs>

      <rect x="12" y="22" width="1032" height="154" rx="12" fill={theme.fill.quaternary} stroke={theme.stroke.tertiary} />
      <text x="28" y="48" fill={theme.text.tertiary} fontSize="11" fontWeight="600">入口层 · 所有入口共享同一后端契约</text>

      <rect x="12" y="194" width="1032" height="270" rx="12" fill={theme.fill.quaternary} stroke={theme.category.red} strokeOpacity="0.55" />
      <text x="28" y="220" fill={theme.category.red} fontSize="11" fontWeight="600">不可插件化内核 · 权威、身份、安全与恢复语义不能被替换</text>

      <rect x="12" y="482" width="1032" height="254" rx="12" fill={theme.fill.quaternary} stroke={theme.stroke.tertiary} />
      <text x="28" y="508" fill={theme.accent.primary} fontSize="11" fontWeight="600">第一方模块 · 初代编译进同一 Go 二进制，按稳定接口协作</text>

      <rect x="12" y="754" width="1032" height="314" rx="12" fill={theme.fill.quaternary} stroke={theme.category.orange} strokeOpacity="0.65" strokeDasharray="7 5" />
      <text x="28" y="780" fill={theme.category.orange} fontSize="11" fontWeight="600">插件能力层 · Definition / Provider / Consumer 三角色，插件不能授予自己权限</text>

      <rect x="12" y="1086" width="1032" height="130" rx="12" fill={theme.fill.quaternary} stroke={theme.category.cyan} strokeOpacity="0.55" />
      <text x="28" y="1112" fill={theme.category.cyan} fontSize="11" fontWeight="600">数据与外部资源 · Core 0 仅依赖 PostgreSQL，其他组件为后续可选扩展</text>

      {architectureEdges.map((edge) => (
        <DiagramEdge key={`${edge.from}-${edge.to}`} edge={edge} focus={focus} />
      ))}
      {architectureNodes.map((node) => (
        <DiagramNode key={node.id} node={node} focus={focus} />
      ))}
    </svg>
  );
}

function Principle({ title, text, color }: { title: string; text: string; color: string }) {
  return (
    <div style={{ borderTop: `3px solid ${color}`, paddingTop: 10 }}>
      <H3 style={{ marginBottom: 6 }}>{title}</H3>
      <Text size="small" tone="secondary">{text}</Text>
    </div>
  );
}

function ProfileCard({ name, modules, purpose }: { name: string; modules: string; purpose: string }) {
  return (
    <Card>
      <CardHeader trailing={<Pill active>{name}</Pill>}>运行 Profile</CardHeader>
      <CardBody>
        <Text size="small" weight="semibold">{modules}</Text>
        <Text size="small" tone="secondary" style={{ marginTop: 6 }}>{purpose}</Text>
      </CardBody>
    </Card>
  );
}

export default function RoomusicModularPluginArchitecture() {
  const theme = useHostTheme();
  const [focus, setFocus] = useState<ArchitectureFocus>("all");
  const focusOptions: Array<{ value: ArchitectureFocus; label: string }> = [
    { value: "all", label: "全景" },
    { value: "kernel", label: "不可替换内核" },
    { value: "modules", label: "第一方模块" },
    { value: "plugins", label: "插件能力" },
    { value: "flow", label: "完整调用流" },
  ];

  return (
    <Stack gap={22} style={{ padding: 24, maxWidth: 1280, margin: "0 auto" }}>
      <Stack gap={8}>
        <Row gap={8} align="center" wrap>
          <Pill active>ROOMusic Core 0</Pill>
          <Pill>Modular Core + Plugin Seams</Pill>
        </Row>
        <H1>核心模块化，能力插件化，权威不可插件化</H1>
        <Text tone="secondary" style={{ maxWidth: 940 }}>
          ROOMusic 借鉴 DeepSeek Harness 的 Capability Seam、类型化事件、可逆注册和 Profile 组合，但不照搬“无特权核心”。涉及音乐实体身份、用户权限、NAS 文件和恢复语义的规则由固定内核掌握。
        </Text>
      </Stack>

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(250px, 1fr))", gap: "18px 24px" }}>
        <Principle
          title="固定内核"
          text="身份、权限、模式判定、Change Set、事务和路径安全不能被插件覆盖。"
          color={theme.category.red}
        />
        <Principle
          title="模块化单体"
          text="初代第一方能力编译进同一 Go 二进制，接口清晰但不承担动态加载成本。"
          color={theme.accent.primary}
        />
        <Principle
          title="插件能力"
          text="Parser、Provider、Agent、Review、Playback 和通知通过稳定契约替换。"
          color={theme.category.orange}
        />
        <Principle
          title="进程外优先"
          text="第三方或高风险插件通过版本化 RPC 运行，避免 Go .so 兼容与进程隔离问题。"
          color={theme.category.purple}
        />
      </div>

      <Card>
        <CardHeader trailing={<Pill active>{focusOptions.find((option) => option.value === focus)?.label}</Pill>}>架构总览</CardHeader>
        <CardBody style={{ padding: "10px 12px 14px", overflowX: "auto" }}>
          <Row gap={7} align="center" wrap style={{ marginBottom: 12 }}>
            <Text size="small" tone="secondary">聚焦：</Text>
            {focusOptions.map((option) => (
              <Button
                key={option.value}
                variant={focus === option.value ? "primary" : "ghost"}
                onClick={() => setFocus(option.value)}
              >
                {option.label}
              </Button>
            ))}
          </Row>
          <ArchitectureDiagram focus={focus} />
        </CardBody>
      </Card>

      <Stack gap={10}>
        <H2>插件契约：借鉴 DSH 的 Capability Seam</H2>
        <Text tone="secondary">
          每项可替换能力都必须同时说明三种角色，只有接口没有消费者、只有实现没有定义，都不算真正的扩展点。
        </Text>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(270px, 1fr))", gap: "18px 24px" }}>
          <Principle
            title="Service Definition"
            text="声明稳定接口、数据类型、错误、协议版本和生命周期；例如 MetadataProvider。"
            color={theme.category.purple}
          />
          <Principle
            title="Service Provider"
            text="实现具体机制或供应商；例如 MusicBrainzProvider 或 OllamaAgentProvider。"
            color={theme.category.orange}
          />
          <Principle
            title="Consumer"
            text="通过接口使用能力；例如 MetadataEnrichmentService，不导入某个具体 provider。"
            color={theme.accent.primary}
          />
        </div>
      </Stack>

      <Stack gap={10}>
        <H2>插件注册必须声明什么</H2>
        <div style={{ display: "grid", gridTemplateColumns: "1fr minmax(300px, 0.85fr)", gap: 18 }}>
          <Card>
            <CardHeader>plugin.yaml</CardHeader>
            <CardBody>
              <Stack gap={5}>
                <Text size="small"><Code>id / version / protocol_version</Code></Text>
                <Text size="small"><Code>capabilities</Code>：插件提供哪些稳定能力</Text>
                <Text size="small"><Code>permissions</Code>：网络、文件、进程和 secret 范围</Text>
                <Text size="small"><Code>health / timeout / resource_limits</Code></Text>
                <Text size="small"><Code>config_schema</Code>：可验证配置，不接收任意字段</Text>
                <Text size="small"><Code>migration</Code>：插件私有数据版本，不能修改核心表</Text>
              </Stack>
            </CardBody>
          </Card>
          <div style={{ borderTop: `3px solid ${theme.category.red}`, paddingTop: 12 }}>
            <H3 style={{ marginBottom: 8 }}>内核强制规则</H3>
            <Text size="small" tone="secondary">插件不能自行声明当前用户是 Operator。</Text>
            <Text size="small" tone="secondary">插件不能降低工具的内核风险下限。</Text>
            <Text size="small" tone="secondary">插件卸载只撤销注册，不冒充业务 rollback。</Text>
            <Text size="small" tone="secondary">副作用仍需 Change Set、Journal 和恢复信息。</Text>
            <Text size="small" tone="secondary">第三方插件默认无数据库、无音乐目录、无 secret 权限。</Text>
          </div>
        </div>
      </Stack>

      <Stack gap={10}>
        <H2>按阶段演进，而不是先造插件市场</H2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(250px, 1fr))", gap: 12 }}>
          <ProfileCard
            name="Core 0"
            modules="稳定接口 + 第一方编译期注册"
            purpose="单二进制、类型安全、便于测试；先交付完整音乐库闭环。"
          />
          <ProfileCard
            name="Core 1"
            modules="版本化 HTTP / gRPC / JSON-RPC 插件"
            purpose="接入第三方 provider、AI worker 和高风险独立进程。"
          />
          <ProfileCard
            name="Core 2"
            modules="WASM 纯计算插件 + 安装管理"
            purpose="承载字符串规范化、规则解析和评分；不承担任意文件或进程操作。"
          />
        </div>
      </Stack>

      <Stack gap={10}>
          <H2>运行 Profile（Core 0 与后续扩展）</H2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(230px, 1fr))", gap: 12 }}>
          <ProfileCard name="core-0" modules="PostgreSQL + Library + Scanner + Web" purpose="当前闭环：进程内扫描、PostgreSQL 搜索，不依赖 Redis、Meilisearch 或 AI。" />
          <ProfileCard name="standard (future)" modules="core-0 + Redis + Meilisearch" purpose="后续后台任务、重试与可替换搜索投影。" />
          <ProfileCard name="agent (future)" modules="standard + Agent + Review Provider" purpose="后续开放 Assistant、Steward 和 Operator 控制面。" />
          <ProfileCard name="full (future)" modules="agent + Playback + External Providers" purpose="后续加入播放、多源 metadata、通知与高级执行器。" />
        </div>
      </Stack>

      <Divider />
      <Card variant="borderless" style={{ background: theme.fill.quaternary }}>
        <CardBody style={{ padding: 16 }}>
          <H2 style={{ marginBottom: 8 }}>最终边界</H2>
          <Text>
            <Text weight="semibold">现在实现：</Text>固定内核、第一方模块、Capability Registry 接口和编译期注册。
          </Text>
          <Text style={{ marginTop: 6 }}>
            <Text weight="semibold">以后实现：</Text>第三方插件安装、进程外协议、WASM、热更新和插件 UI 贡献。
          </Text>
          <Text style={{ marginTop: 6 }}>
            <Text weight="semibold">始终禁止：</Text>插件绕过 Authority、修改核心表语义、授予自身权限或把卸载当成数据回滚。
          </Text>
        </CardBody>
      </Card>

      <Text size="small" tone="tertiary">
        参考：DeepSeek Harness Architecture、Cordis Primer、Capability Seams、Extension Cookbook 和 Safety Notice。ROOMusic 只借鉴组合范式，不直接依赖 DSH/Cordis 运行时。
      </Text>
    </Stack>
  );
}
