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

type DiagramFocus = "all" | "core" | "agent" | "extension";
type NodeCategory = "core" | "agent" | "extension" | "data";

interface DiagramNode {
  id: string;
  title: string;
  details: string[];
  x: number;
  y: number;
  width: number;
  height: number;
  category: NodeCategory;
  dashed?: boolean;
}

interface DiagramConnection {
  from: string;
  to: string;
  label: string;
  kind?: "primary" | "secondary" | "extension";
}

const diagramNodes: DiagramNode[] = [
  {
    id: "web",
    title: "Web UI",
    details: ["React + TypeScript", "Library / Search / Auth", "Operation status"],
    x: 44,
    y: 76,
    width: 210,
    height: 106,
    category: "core",
  },
  {
    id: "admin-console",
    title: "Agent / Admin Console",
    details: ["Assistant / Steward / Operator", "Plan preview and execution", "Explicit mode switch"],
    x: 278,
    y: 76,
    width: 234,
    height: 106,
    category: "agent",
  },
  {
    id: "api",
    title: "API Gateway",
    details: ["REST / GraphQL", "Session and request_id", "One public application port"],
    x: 44,
    y: 248,
    width: 210,
    height: 110,
    category: "core",
  },
  {
    id: "mode-router",
    title: "Mode Router",
    details: ["Assistant: user approval", "Steward: review subagent", "Operator: direct execution"],
    x: 278,
    y: 248,
    width: 234,
    height: 110,
    category: "agent",
  },
  {
    id: "library-services",
    title: "Library Services",
    details: ["Scan orchestration", "Release Graph read model", "Metadata and evidence"],
    x: 536,
    y: 248,
    width: 220,
    height: 110,
    category: "core",
  },
  {
    id: "change-management",
    title: "Change Management",
    details: ["Change Set", "Operation Journal", "Checkpoint and rollback"],
    x: 780,
    y: 248,
    width: 220,
    height: 110,
    category: "core",
  },
  {
    id: "tool-authority",
    title: "Tool Registry + Authority",
    details: ["Allowlisted tools", "Scope and path validation", "Revision and transaction"],
    x: 536,
    y: 402,
    width: 220,
    height: 110,
    category: "core",
  },
  {
    id: "review-subagent",
    title: "Review Subagent",
    details: ["Steward-only review path", "Evidence / risk / impact", "Structured approval result"],
    x: 780,
    y: 402,
    width: 220,
    height: 110,
    category: "agent",
    dashed: true,
  },
  {
    id: "postgres",
    title: "PostgreSQL Authority",
    details: ["Users and sessions", "ReleaseGroup -> Track", "Evidence and operation history"],
    x: 44,
    y: 590,
    width: 210,
    height: 106,
    category: "data",
  },
  {
    id: "redis",
    title: "Redis Task Runtime",
    details: ["Scan jobs", "Retryable background work", "Optional in the first slice"],
    x: 278,
    y: 590,
    width: 234,
    height: 106,
    category: "data",
  },
  {
    id: "search",
    title: "Search Projection",
    details: ["Meilisearch index", "Derived read model", "Rebuildable, not authority"],
    x: 536,
    y: 590,
    width: 220,
    height: 106,
    category: "data",
  },
  {
    id: "music-files",
    title: "Music Library",
    details: ["NAS / local filesystem", "Read-only scan input", "No arbitrary path from UI"],
    x: 780,
    y: 590,
    width: 220,
    height: 106,
    category: "data",
  },
  {
    id: "extension-runtime",
    title: "Replaceable Extension Runtime",
    details: ["AI provider adapter", "Metadata providers", "Playback / plugins / webhooks"],
    x: 278,
    y: 776,
    width: 478,
    height: 106,
    category: "extension",
    dashed: true,
  },
  {
    id: "recovery-storage",
    title: "Recovery Storage",
    details: ["File manifest and hash", "Quarantine before purge", "Backup / restore later"],
    x: 780,
    y: 776,
    width: 220,
    height: 106,
    category: "extension",
    dashed: true,
  },
];

const diagramConnections: DiagramConnection[] = [
  { from: "web", to: "api", label: "HTTPS / session", kind: "primary" },
  { from: "admin-console", to: "mode-router", label: "mode + intent", kind: "primary" },
  { from: "api", to: "mode-router", label: "authenticated request", kind: "primary" },
  { from: "mode-router", to: "library-services", label: "tool request", kind: "primary" },
  { from: "mode-router", to: "review-subagent", label: "Steward only", kind: "extension" },
  { from: "library-services", to: "change-management", label: "planned change", kind: "primary" },
  { from: "library-services", to: "tool-authority", label: "domain command", kind: "primary" },
  { from: "change-management", to: "tool-authority", label: "execute / revert", kind: "primary" },
  { from: "review-subagent", to: "tool-authority", label: "review result", kind: "extension" },
  { from: "library-services", to: "postgres", label: "authority writes", kind: "primary" },
  { from: "library-services", to: "redis", label: "background jobs", kind: "secondary" },
  { from: "library-services", to: "search", label: "derived projection", kind: "secondary" },
  { from: "library-services", to: "music-files", label: "read-only scan", kind: "primary" },
  { from: "change-management", to: "postgres", label: "journal / checkpoint", kind: "secondary" },
  { from: "change-management", to: "recovery-storage", label: "manifest / quarantine", kind: "extension" },
  { from: "tool-authority", to: "extension-runtime", label: "stable contracts", kind: "extension" },
  { from: "review-subagent", to: "extension-runtime", label: "replaceable adapter", kind: "extension" },
];

function getNodeById(nodeId: string): DiagramNode {
  const node = diagramNodes.find((candidate) => candidate.id === nodeId);
  if (!node) {
    throw new Error(`Unknown architecture node: ${nodeId}`);
  }
  return node;
}

function isNodeVisible(node: DiagramNode, focus: DiagramFocus): boolean {
  if (focus === "all") {
    return true;
  }
  if (focus === "core") {
    return node.category === "core" || node.category === "data";
  }
  if (focus === "agent") {
    return node.category === "agent" || node.category === "core";
  }
  return node.category === "extension";
}

function getConnectionOpacity(connection: DiagramConnection, focus: DiagramFocus): number {
  if (focus === "all") {
    return 1;
  }
  if (focus === "core") {
    return connection.kind === "extension" ? 0.16 : 1;
  }
  if (focus === "agent") {
    return connection.kind === "extension" || connection.from === "mode-router" || connection.to === "mode-router" ? 1 : 0.22;
  }
  return connection.kind === "extension" ? 1 : 0.14;
}

function getCategoryColor(theme: ReturnType<typeof useHostTheme>, category: NodeCategory): string {
  if (category === "agent") {
    return theme.category.purple;
  }
  if (category === "extension") {
    return theme.category.orange;
  }
  if (category === "data") {
    return theme.category.cyan;
  }
  return theme.accent.primary;
}

function renderConnection(theme: ReturnType<typeof useHostTheme>, connection: DiagramConnection, focus: DiagramFocus) {
  const sourceNode = getNodeById(connection.from);
  const targetNode = getNodeById(connection.to);
  const isHorizontalConnection = Math.abs(sourceNode.y - targetNode.y) < 20;
  const sourceIsLeftOfTarget = sourceNode.x < targetNode.x;
  const sourceX = isHorizontalConnection
    ? sourceIsLeftOfTarget
      ? sourceNode.x + sourceNode.width
      : sourceNode.x
    : sourceNode.x + sourceNode.width / 2;
  const sourceY = isHorizontalConnection ? sourceNode.y + sourceNode.height / 2 : sourceNode.y + sourceNode.height;
  const targetX = isHorizontalConnection
    ? sourceIsLeftOfTarget
      ? targetNode.x
      : targetNode.x + targetNode.width
    : targetNode.x + targetNode.width / 2;
  const targetY = isHorizontalConnection ? targetNode.y + targetNode.height / 2 : targetNode.y;
  const opacity = getConnectionOpacity(connection, focus);
  const strokeColor = connection.kind === "extension" ? theme.category.orange : theme.stroke.secondary;
  const labelX = (sourceX + targetX) / 2;
  const labelY = isHorizontalConnection ? sourceY - 10 : sourceY + (targetY - sourceY) / 2 - 5;
  const pathData = isHorizontalConnection
    ? `M ${sourceX} ${sourceY} C ${sourceX + (targetX - sourceX) / 2} ${sourceY}, ${targetX - (targetX - sourceX) / 2} ${targetY}, ${targetX} ${targetY}`
    : `M ${sourceX} ${sourceY} C ${sourceX} ${sourceY + (targetY - sourceY) / 2}, ${targetX} ${targetY - (targetY - sourceY) / 2}, ${targetX} ${targetY}`;

  return (
    <g key={`${connection.from}-${connection.to}`} opacity={opacity}>
      <path
        d={pathData}
        fill="none"
        stroke={strokeColor}
        strokeDasharray={connection.kind === "extension" ? "5 4" : undefined}
        strokeWidth={connection.kind === "primary" ? 1.8 : 1.2}
        markerEnd="url(#roomusic-arrow)"
      />
      <text
        x={labelX}
        y={labelY}
        textAnchor="middle"
        fill={theme.text.tertiary}
        fontSize="10"
        fontFamily="ui-sans-serif, system-ui, sans-serif"
      >
        {connection.label}
      </text>
    </g>
  );
}

function renderNode(theme: ReturnType<typeof useHostTheme>, node: DiagramNode, focus: DiagramFocus) {
  const categoryColor = getCategoryColor(theme, node.category);
  const visible = isNodeVisible(node, focus);
  const opacity = visible ? 1 : 0.2;
  const fillColor = node.category === "extension" ? theme.fill.secondary : theme.bg.elevated;

  return (
    <g key={node.id} opacity={opacity}>
      <rect
        x={node.x}
        y={node.y}
        width={node.width}
        height={node.height}
        rx="8"
        fill={fillColor}
        stroke={categoryColor}
        strokeOpacity={visible ? 0.75 : 0.22}
        strokeDasharray={node.dashed ? "6 4" : undefined}
        strokeWidth="1.4"
      />
      <rect x={node.x} y={node.y} width="5" height={node.height} rx="2" fill={categoryColor} opacity={visible ? 0.9 : 0.25} />
      <text
        x={node.x + 18}
        y={node.y + 24}
        fill={theme.text.primary}
        fontSize="14"
        fontWeight="600"
        fontFamily="ui-sans-serif, system-ui, sans-serif"
      >
        {node.title}
      </text>
      {node.details.map((detail, detailIndex) => (
        <text
          key={`${node.id}-${detail}`}
          x={node.x + 18}
          y={node.y + 48 + detailIndex * 17}
          fill={theme.text.secondary}
          fontSize="11"
          fontFamily="ui-sans-serif, system-ui, sans-serif"
        >
          {detail}
        </text>
      ))}
    </g>
  );
}

function ArchitectureDiagram({ theme, focus }: { theme: ReturnType<typeof useHostTheme>; focus: DiagramFocus }) {
  return (
    <svg
      viewBox="0 0 1044 930"
      role="img"
      aria-label="ROOMusic 初代核心架构图"
      style={{ width: "100%", minWidth: 780, display: "block" }}
    >
      <defs>
        <marker id="roomusic-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
          <path d="M 0 0 L 8 4 L 0 8 z" fill={theme.stroke.secondary} />
        </marker>
      </defs>

      <rect x="12" y="26" width="1020" height="172" rx="12" fill={theme.fill.quaternary} stroke={theme.stroke.tertiary} />
      <text x="30" y="52" fill={theme.text.tertiary} fontSize="11" fontWeight="600" fontFamily="ui-sans-serif, system-ui, sans-serif">
        访问层 · 用户只接触产品入口，不接触数据库或文件路径
      </text>

      <rect x="12" y="214" width="1020" height="332" rx="12" fill={theme.fill.quaternary} stroke={theme.stroke.tertiary} />
      <text x="30" y="240" fill={theme.text.tertiary} fontSize="11" fontWeight="600" fontFamily="ui-sans-serif, system-ui, sans-serif">
        Go 应用层 · 业务能力、Agent 模式和唯一执行权威
      </text>

      <rect x="12" y="562" width="1020" height="164" rx="12" fill={theme.fill.quaternary} stroke={theme.stroke.tertiary} />
      <text x="30" y="588" fill={theme.text.tertiary} fontSize="11" fontWeight="600" fontFamily="ui-sans-serif, system-ui, sans-serif">
        数据与运行时 · PostgreSQL 是权威，搜索和队列都是可重建/可替换的辅助组件
      </text>

      <rect x="12" y="748" width="1020" height="156" rx="12" fill={theme.fill.quaternary} stroke={theme.category.orange} strokeOpacity="0.65" strokeDasharray="7 5" />
      <text x="30" y="774" fill={theme.category.orange} fontSize="11" fontWeight="600" fontFamily="ui-sans-serif, system-ui, sans-serif">
        扩展层 · 初代不绑定具体供应商，先锁定稳定契约
      </text>

      {diagramConnections.map((connection) => renderConnection(theme, connection, focus))}
      {diagramNodes.map((node) => renderNode(theme, node, focus))}
    </svg>
  );
}

function ModeCard({
  title,
  tone,
  flow,
  description,
}: {
  title: string;
  tone: string;
  flow: string;
  description: string;
}) {
  return (
    <Card style={{ height: "100%" }}>
      <CardHeader trailing={<Pill active>{title}</Pill>}>执行模式</CardHeader>
      <CardBody>
        <div style={{ borderLeft: `3px solid ${tone}`, paddingLeft: 12 }}>
          <Text weight="semibold">{flow}</Text>
          <Text size="small" tone="secondary" style={{ marginTop: 6 }}>
            {description}
          </Text>
        </div>
      </CardBody>
    </Card>
  );
}

function ModuleGroup({ title, items }: { title: string; items: string[] }) {
  const theme = useHostTheme();

  return (
    <div style={{ borderTop: `2px solid ${theme.stroke.secondary}`, padding: "12px 4px 4px" }}>
      <H3 style={{ marginBottom: 8 }}>{title}</H3>
      <Stack gap={5}>
        {items.map((item) => (
          <Text key={`${title}-${item}`} size="small" tone="secondary">
            {item}
          </Text>
        ))}
      </Stack>
    </div>
  );
}

function ExtensionCard({
  title,
  contract,
  examples,
}: {
  title: string;
  contract: string;
  examples: string;
}) {
  const theme = useHostTheme();

  return (
    <div style={{ borderTop: `2px dashed ${theme.category.orange}`, padding: "12px 4px 6px" }}>
      <H3 style={{ marginBottom: 6 }}>{title}</H3>
      <Text size="small" tone="secondary">
        契约：<Code>{contract}</Code>
      </Text>
      <Text size="small" tone="secondary" style={{ marginTop: 6 }}>
        可替换实现：{examples}
      </Text>
    </div>
  );
}

export default function RoomusicCoreArchitecture() {
  const theme = useHostTheme();
  const [focus, setFocus] = useState<DiagramFocus>("all");

  const focusOptions: Array<{ id: DiagramFocus; label: string }> = [
    { id: "all", label: "全景" },
    { id: "core", label: "初代核心" },
    { id: "agent", label: "Agent 流程" },
    { id: "extension", label: "扩展点" },
  ];

  return (
    <Stack gap={20} style={{ padding: 24, maxWidth: 1280, margin: "0 auto" }}>
      <Stack gap={8}>
        <Row gap={10} align="center" wrap>
          <Pill active>ROOMusic Core 0</Pill>
          <Text size="small" tone="secondary">从 V0 继承目标，不继承膨胀实现</Text>
        </Row>
        <H1>初代版本架构：稳定核心 + 受控执行 + 可替换扩展</H1>
        <Text tone="secondary" style={{ maxWidth: 920 }}>
          初代版本先把本地音乐库、Release Graph、证据来源、前后端闭环和变更恢复能力做扎实。Agent 不直接拥有数据真相；所有模式最终进入后端注册工具和 Authority，扩展能力通过稳定契约接入。
        </Text>
      </Stack>

      <Card>
        <CardHeader trailing={<Pill active>{focus === "all" ? "全景视图" : "已聚焦"}</Pill>}>架构图</CardHeader>
        <CardBody style={{ padding: "8px 12px 14px", overflowX: "auto" }}>
          <Row gap={8} align="center" wrap style={{ marginBottom: 12 }}>
            <Text size="small" tone="secondary">查看：</Text>
            {focusOptions.map((option) => (
              <Button
                key={option.id}
                variant={focus === option.id ? "primary" : "ghost"}
                onClick={() => setFocus(option.id)}
              >
                {option.label}
              </Button>
            ))}
          </Row>
          <ArchitectureDiagram theme={theme} focus={focus} />
        </CardBody>
      </Card>

      <Stack gap={10}>
        <H2>初代版本包含的模块</H2>
        <Text tone="secondary">
          下面的模块是 Core 0 的最小完整闭环；它们足以让系统从登录、扫描、入库、浏览到受控变更真正运行起来。
        </Text>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))", gap: "18px 24px" }}>
          <ModuleGroup
            title="前端产品层"
            items={["登录与会话", "Library / Search / Release 详情", "扫描进度、错误和操作状态", "Agent 模式入口与操作计划预览"]}
          />
          <ModuleGroup
            title="Go 后端核心"
            items={["API Gateway、认证和权限", "扫描编排与只读文件访问", "ReleaseGroup → Track 数据模型", "字段来源、scan_run 和证据摘要"]}
          />
          <ModuleGroup
            title="变更与恢复"
            items={["Change Set：一次完整业务变更", "Operation Journal：持久操作历史", "Checkpoint：执行前恢复点", "Reversible Executor：逆向执行"]}
          />
          <ModuleGroup
            title="基础设施"
            items={["PostgreSQL：唯一业务权威", "Redis：后台任务与重试", "Meilisearch：可重建搜索投影", "Docker Compose：可复现依赖环境"]}
          />
          <ModuleGroup
            title="Agent 控制面"
            items={["Assistant：人工批准危险操作", "Steward：Review Subagent 审查", "Operator：管理员直接执行", "三种模式共享 Authority 和执行器"]}
          />
          <ModuleGroup
            title="质量与可观测性"
            items={["JSON 结构化日志", "request_id / scan_run_id / operation_id", "gofmt、test、vet、lint、typecheck", "关键路径的真实依赖验证"]}
          />
        </div>
      </Stack>

      <Stack gap={10}>
        <H2>三种模式如何共用一套执行基础</H2>
        <Text tone="secondary">
          模式的差异只体现在“谁可以让操作进入执行阶段”，而不是每种模式拥有一套互不兼容的后端。这样后续加 UI、CLI 或自动任务时，不会复制权限和回滚逻辑。
        </Text>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))", gap: 12 }}>
          <ModeCard
            title="Assistant"
            tone={theme.category.blue}
            flow="计划 → 用户批准 → Authority → 执行"
            description="适合普通用户和高风险整理。Assistant 只能提出计划，不能自己批准。"
          />
          <ModeCard
            title="Steward"
            tone={theme.category.purple}
            flow="计划 → Review Subagent → Authority → 执行"
            description="适合受策略约束的自动整理。审查结果必须结构化，不能由主 Agent 自我批准。"
          />
          <ModeCard
            title="Operator"
            tone={theme.category.orange}
            flow="计划 → Authority → 直接执行"
            description="管理员显式进入后跳过二次批准，但仍必须使用白名单工具、参数校验和操作记录。"
          />
        </div>
      </Stack>

      <Stack gap={10}>
        <H2>可拓展性体现在哪里</H2>
        <Text tone="secondary">
          可拓展性不是提前把所有功能写进 Core 0，而是把变化隔离在稳定边界之后。初代先固定数据权威、工具契约和变更生命周期，未来实现可以替换，不必重写核心。
        </Text>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", gap: "18px 24px" }}>
          <ExtensionCard
            title="Agent / 模型适配器"
            contract="AgentRequest / AgentResult"
            examples="本地模型、OpenAI-compatible provider、未来其他模型"
          />
          <ExtensionCard
            title="审查子 Agent"
            contract="ReviewRequest / ReviewDecision"
            examples="规则审查器、模型审查器、人工复核队列"
          />
          <ExtensionCard
            title="工具注册表"
            contract="ToolDefinition / ToolCall"
            examples="library_search、metadata_edit、file_move、tag_write"
          />
          <ExtensionCard
            title="数据投影与事件"
            contract="DomainChange / ProjectionEvent"
            examples="Meilisearch、webhook、实时订阅、外部索引"
          />
          <ExtensionCard
            title="媒体与 metadata provider"
            contract="ProviderAdapter"
            examples="MusicBrainz、Discogs、Last.fm、QQ Music、Cover Art"
          />
          <ExtensionCard
            title="执行器"
            contract="ReversibleOperation"
            examples="metadata overlay、文件移动、隔离删除、tag write-back"
          />
        </div>
      </Stack>

      <Divider />
      <Card variant="borderless" style={{ background: theme.fill.quaternary }}>
        <CardBody style={{ padding: 16 }}>
          <H2 style={{ marginBottom: 8 }}>初代版本的边界判断</H2>
          <Row gap={8} align="start" wrap>
            <Pill active>现在做</Pill>
            <Text>登录、扫描、Release Graph、来源证据、搜索、操作变更集、恢复点和三种模式的执行入口。</Text>
          </Row>
          <Row gap={8} align="start" wrap style={{ marginTop: 8 }}>
            <Pill>以后接入</Pill>
            <Text>真实 AI provider、Review Subagent、播放、更多 metadata provider、文件写回和更复杂的自动化。</Text>
          </Row>
          <Row gap={8} align="start" wrap style={{ marginTop: 8 }}>
            <Pill>不提前承诺</Pill>
            <Text>完整 Event Sourcing、多 Agent 协作、任意 shell、无边界文件访问和把模型本身当作业务权威。</Text>
          </Row>
        </CardBody>
      </Card>
    </Stack>
  );
}
