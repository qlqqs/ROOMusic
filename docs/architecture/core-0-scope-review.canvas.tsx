import {
  Card,
  CardBody,
  CardHeader,
  H1,
  H2,
  H3,
  Pill,
  Row,
  Stack,
  Text,
  useHostTheme,
} from "cursor/canvas";

interface ScopeItem {
  title: string;
  currentScope: string;
  recommendation: "keep" | "trim" | "defer";
  reason: string;
}

const scopeItems: ScopeItem[] = [
  {
    title: "单体 Go + React + PostgreSQL + REST",
    currentScope: "工程骨架、同源生产部署、版本化 REST、PostgreSQL 唯一权威",
    recommendation: "keep",
    reason: "与架构图的模块化单体方向一致，是后续能力的必要承载面。",
  },
  {
    title: "初始化与认证",
    currentScope: "初始管理员、普通用户管理、禁用、会话撤销、CSRF 防护",
    recommendation: "trim",
    reason: "保留单管理员初始化、登录和会话撤销；普通用户管理可在首个浏览闭环之后补齐。",
  },
  {
    title: "目录与只读扫描",
    currentScope: "白名单、realpath、链接策略、全局串行扫描、软缺失对账",
    recommendation: "keep",
    reason: "这是 NAS 安全边界和可重扫性的核心，不应为了赶进度削弱。",
  },
  {
    title: "首批解析能力",
    currentScope: "FLAC、MP3、OGG、Opus、WAV、CUE、多碟结构",
    recommendation: "trim",
    reason: "首个纵向切片建议先交付 FLAC、MP3 与普通多碟目录；CUE 和其余格式按独立增量验收。",
  },
  {
    title: "Release Graph 与来源解释",
    currentScope: "ReleaseGroup -> Release -> Medium -> Track、字段来源、稳定重扫身份",
    recommendation: "keep",
    reason: "这是 ROOMusic 区别于文件浏览器的产品核心，但来源模型应先保持最小。",
  },
  {
    title: "封面闭环",
    currentScope: "目录图、内嵌图、hash 存储、鉴权资源 API、缓存语义",
    recommendation: "defer",
    reason: "有用户价值，但不是证明扫描、图谱、浏览闭环成立的必要条件，且引入图片解析与文件缓存面。",
  },
  {
    title: "Change Set 与 Operation Journal",
    currentScope: "目录新增、停用、恢复、revision、幂等、before/after、逆操作",
    recommendation: "trim",
    reason: "保留 revision、幂等和审计事件；通用 Change Set/恢复框架应在第二种真实写操作出现后再抽象。",
  },
  {
    title: "Music Steward 三模式",
    currentScope: "仅定义 Assistant、Steward、Operator 契约，不运行 Agent",
    recommendation: "defer",
    reason: "可留在长期架构文档，但不应成为 Core 0 产品验收或数据库实现负担。",
  },
];

function getRecommendationLabel(recommendation: ScopeItem["recommendation"]): string {
  if (recommendation === "keep") return "保留";
  if (recommendation === "trim") return "收窄";
  return "后移";
}

export default function CoreZeroScopeReview() {
  const theme = useHostTheme();
  const recommendationColors = {
    keep: theme.category.green,
    trim: theme.category.orange,
    defer: theme.text.tertiary,
  };

  return (
    <Stack gap={24} style={{ padding: 24, maxWidth: 1180, margin: "0 auto" }}>
      <Stack gap={8}>
        <Row gap={8} align="center" wrap>
          <Pill active>架构审查</Pill>
          <Pill>Core 0 Planning</Pill>
        </Row>
        <H1>方向正确，版本边界清晰，但单次交付范围过大</H1>
        <Text tone="secondary" style={{ maxWidth: 900 }}>
          当前 PRD 与架构图在模块化单体、固定后端权威、PostgreSQL-only、REST-first 和只读音乐目录上高度一致。主要风险不是架构方向，而是把多个可独立验收的高风险系统一次性交付。
        </Text>
      </Stack>

      <div style={{ display: "grid", gridTemplateColumns: "1.35fr 1fr", gap: 20 }}>
        <div style={{ borderTop: `4px solid ${theme.category.orange}`, paddingTop: 14 }}>
          <H2>结论</H2>
          <Text style={{ marginTop: 8 }}>
            Core 0 作为产品目标是合理的；作为一个 Trellis 实现任务不合理。建议把当前任务改为父任务或版本级验收合同，再用 3 至 5 个子任务交付纵向切片。
          </Text>
        </div>
        <Card>
          <CardHeader>范围信号</CardHeader>
          <CardBody>
            <Stack gap={7}>
              <Text size="small">8 个实施阶段</Text>
              <Text size="small">后端、前端、数据库、文件系统与安全五类边界</Text>
              <Text size="small">扫描、认证、图片、恢复四个独立风险中心</Text>
              <Text size="small">当前无业务工程骨架，所有能力均为绿地建设</Text>
            </Stack>
          </CardBody>
        </Card>
      </div>

      <Stack gap={12}>
        <H2>逐项范围裁剪</H2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(330px, 1fr))", gap: 14 }}>
          {scopeItems.map((scopeItem) => (
            <Card key={scopeItem.title}>
              <CardHeader
                trailing={
                  <span style={{ color: recommendationColors[scopeItem.recommendation], fontWeight: 600 }}>
                    {getRecommendationLabel(scopeItem.recommendation)}
                  </span>
                }
              >
                {scopeItem.title}
              </CardHeader>
              <CardBody>
                <Text size="small" weight="semibold">当前：{scopeItem.currentScope}</Text>
                <Text size="small" tone="secondary" style={{ marginTop: 8 }}>{scopeItem.reason}</Text>
              </CardBody>
            </Card>
          ))}
        </div>
      </Stack>

      <Stack gap={12}>
        <H2>建议的 Core 0 交付树</H2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(4, 1fr)", gap: 14 }}>
          <div style={{ borderTop: `3px solid ${theme.accent.primary}`, paddingTop: 12 }}>
            <H3>C0-A 基础与身份</H3>
            <Text size="small" tone="secondary">Go/React/PostgreSQL、迁移、健康检查、单管理员 setup/login/session。</Text>
          </div>
          <div style={{ borderTop: `3px solid ${theme.accent.primary}`, paddingTop: 12 }}>
            <H3>C0-B 安全扫描</H3>
            <Text size="small" tone="secondary">allowed roots、FLAC/MP3、串行 scan run、诊断、完整成功才 missing。</Text>
          </div>
          <div style={{ borderTop: `3px solid ${theme.accent.primary}`, paddingTop: 12 }}>
            <H3>C0-C 图谱浏览</H3>
            <Text size="small" tone="secondary">最小 Release Graph、来源解释、REST 列表/详情/搜索、对应前端。</Text>
          </div>
          <div style={{ borderTop: `3px solid ${theme.category.orange}`, paddingTop: 12 }}>
            <H3>C0-D 加固增量</H3>
            <Text size="small" tone="secondary">普通用户、其余格式、CUE、封面、目录操作审计与恢复，逐项独立验收。</Text>
          </div>
        </div>
      </Stack>

      <Stack gap={8}>
        <H2>与架构图的关键差异</H2>
        <Text tone="secondary">
          架构图正确地把 Plugin Host、Capability Registry、Agent、Redis 和 Meilisearch标记为未来扩展；实现计划也没有把它们带入 Core 0。仍需进一步避免把未来 Tool Authority、Mode Kernel 和通用恢复框架提前物化。Core 0 只需要为未来保留清晰边界，不需要先实现扩展基础设施。
        </Text>
      </Stack>

      <Text size="small" tone="tertiary">
        依据：当前 Trellis PRD、design.md、implement.md 与 roomusic-modular-plugin-architecture.canvas.tsx；审查日期 2026-09-01。
      </Text>
    </Stack>
  );
}
