<template>
  <div class="dt">
    <!-- 页头 -->
    <header class="dt__header">
      <div>
        <h1 class="dt__title">Hify 设计系统</h1>
        <p class="dt__subtitle">
          浅底 + 科技感点缀 · token 预览与验收页 · 规范见
          <code>docs/design/design-system.md</code>
        </p>
      </div>
      <div class="dt__gradient" />
    </header>

    <!-- 品牌主色色阶 -->
    <section class="dt__section">
      <h2 class="dt__section-title">主色 · 品牌蓝紫（Linear 紫）</h2>
      <div class="dt__swatches">
        <div v-for="s in primaryScale" :key="s.step" class="dt__swatch">
          <div
            class="dt__swatch-color"
            :style="{ backgroundColor: `var(${s.token})` }"
          />
          <div class="dt__swatch-meta">
            <span>{{ s.step }}</span>
            <code>{{ s.hex }}</code>
          </div>
        </div>
      </div>

      <h2 class="dt__section-title dt__section-title--sub">
        辅色 · 青色（品牌点缀 / 数据高亮，不兼语义状态）
      </h2>
      <div class="dt__swatches">
        <div v-for="s in accentScale" :key="s.step" class="dt__swatch">
          <div
            class="dt__swatch-color"
            :style="{ backgroundColor: `var(${s.token})` }"
          />
          <div class="dt__swatch-meta">
            <span>{{ s.step }}</span>
            <code>{{ s.hex }}</code>
          </div>
        </div>
      </div>
    </section>

    <!-- 语义色 -->
    <section class="dt__section">
      <h2 class="dt__section-title">语义色（状态专用）</h2>
      <div class="dt__semantic-list">
        <div
          v-for="s in semantics"
          :key="s.name"
          class="dt__semantic"
          :style="{
            '--sem-base': `var(--hf-${s.name})`,
            '--sem-soft': `var(--hf-${s.name}-soft)`,
            '--sem-border': `var(--hf-${s.name}-border)`,
          }"
        >
          <span class="dt__semantic-solid">{{ s.name }}</span>
          <span class="dt__semantic-soft">{{ s.label }}</span>
          <span class="dt__semantic-desc">{{ s.desc }}</span>
        </div>
      </div>
    </section>

    <!-- 背景 / 文字 / 边框 -->
    <section class="dt__section">
      <h2 class="dt__section-title">背景 · 文字 · 边框色阶</h2>
      <div class="dt__layers">
        <div class="dt__layer-page">
          <code>--hf-bg-page</code>
          <div class="dt__layer-container">
            <code>--hf-bg-container</code>
            <div class="dt__layer-row">
              <div class="dt__layer-subtle">
                <code>--hf-bg-subtle</code>
              </div>
              <div class="dt__layer-muted">
                <code>--hf-bg-muted</code>
              </div>
            </div>
          </div>
        </div>
        <div class="dt__layer-texts">
          <p style="color: var(--hf-text-1)">主文字 --hf-text-1</p>
          <p style="color: var(--hf-text-2)">次要文字 --hf-text-2</p>
          <p style="color: var(--hf-text-3)">弱化文字 --hf-text-3</p>
          <p style="color: var(--hf-text-4)">禁用文字 --hf-text-4</p>
          <p>
            <a class="dt__link" href="#">文字链接 --hf-text-link</a>
          </p>
          <div class="dt__border-row">
            <div class="dt__border-line dt__border-line--1" />
            <div class="dt__border-line dt__border-line--2" />
            <div class="dt__border-line dt__border-line--3" />
            <code>border-1 / 2 / 3</code>
          </div>
        </div>
      </div>
    </section>

    <!-- 按钮 -->
    <section class="dt__section">
      <h2 class="dt__section-title">按钮</h2>
      <div class="dt__row">
        <el-button type="primary">主操作</el-button>
        <el-button>默认</el-button>
        <el-button type="success">成功</el-button>
        <el-button type="warning">警告</el-button>
        <el-button type="danger">危险</el-button>
        <el-button type="info">信息</el-button>
        <el-button type="primary" plain>主色描边</el-button>
        <el-button type="primary" link>链接按钮</el-button>
        <el-button type="primary" disabled>禁用</el-button>
      </div>
      <div class="dt__row">
        <button type="button" class="dt__btn-glow">主按钮 hover 微光</button>
        <button type="button" class="dt__btn-gradient">品牌渐变 CTA</button>
        <el-button type="danger" plain>删除确认</el-button>
      </div>
    </section>

    <!-- 表单 -->
    <section class="dt__section">
      <h2 class="dt__section-title">表单控件</h2>
      <div class="dt__form-grid">
        <el-input placeholder="提供商名称" />
        <el-select placeholder="选择模型">
          <el-option label="gpt-4o" value="gpt-4o" />
          <el-option label="claude-sonnet-4-5" value="claude-sonnet-4-5" />
          <el-option label="qwen3-max" value="qwen3-max" />
        </el-select>
        <el-input-number :model-value="16" :min="1" :max="64" />
        <el-switch :model-value="true" active-text="启用语义缓存" />
        <el-radio-group :model-value="'stream'">
          <el-radio value="stream">流式</el-radio>
          <el-radio value="once">一次性</el-radio>
        </el-radio-group>
        <el-checkbox-group :model-value="['mcp']">
          <el-checkbox value="mcp">MCP 工具</el-checkbox>
          <el-checkbox value="rag">知识库</el-checkbox>
        </el-checkbox-group>
        <el-slider :model-value="40" :max="100" class="dt__slider" />
      </div>
    </section>

    <!-- 表格 + 状态标签 + 分页 -->
    <section class="dt__section">
      <h2 class="dt__section-title">表格 · 状态标签 · 分页</h2>
      <el-table :data="providerRows" stripe>
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column prop="kind" label="类型" width="120" />
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-tag :type="statusMeta[row.status as ProviderStatus].type" size="small">
              {{ statusMeta[row.status as ProviderStatus].label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="models" label="模型数" width="100" align="right" />
        <el-table-column prop="tokensToday" label="今日 tokens" width="140" align="right">
          <template #default="{ row }">
            <span class="dt__mono">{{ row.tokensToday }}</span>
          </template>
        </el-table-column>
      </el-table>
      <div class="dt__pagination">
        <el-pagination
          layout="total, prev, pager, next"
          :total="47"
          :page-size="20"
          :current-page="1"
        />
      </div>
    </section>

    <!-- 圆角与阴影 -->
    <section class="dt__section">
      <h2 class="dt__section-title">圆角 · 阴影</h2>
      <div class="dt__tiles">
        <div
          v-for="r in radii"
          :key="r.token"
          class="dt__tile"
          :style="{ borderRadius: `var(${r.token})` }"
        >
          {{ r.label }}
        </div>
      </div>
      <div class="dt__tiles dt__tiles--shadow">
        <div
          v-for="s in shadows"
          :key="s.token"
          class="dt__tile dt__tile--flat"
          :style="{ boxShadow: `var(${s.token})` }"
        >
          {{ s.label }}
        </div>
      </div>
    </section>

    <!-- 动效 -->
    <section class="dt__section">
      <h2 class="dt__section-title">动效（点击方块触发）</h2>
      <div class="dt__motion">
        <button
          type="button"
          class="dt__motion-box"
          :class="{ 'dt__motion-box--moved': moved }"
          @click="moved = !moved"
        >
          ease-out · 300ms
        </button>
        <button
          type="button"
          class="dt__motion-box dt__motion-box--spring"
          :class="{ 'dt__motion-box--moved': moved }"
          @click="moved = !moved"
        >
          spring · 300ms
        </button>
        <p class="dt__motion-note">
          只动 transform/opacity；表格行不做动画；hover 变色用 120ms
        </p>
      </div>
    </section>

    <!-- 字体 -->
    <section class="dt__section">
      <h2 class="dt__section-title">字体</h2>
      <div v-for="f in fontSizes" :key="f.token" class="dt__font-row">
        <code class="dt__font-token">{{ f.token }}</code>
        <span :style="{ fontSize: `var(${f.token})` }">Hify AI Agent 开发平台</span>
      </div>
      <div class="dt__font-mono">
        <code>sk-hify-••••••••</code>
        <code>tokens: 1,024 / 4,096</code>
        <code>TTFT 358ms</code>
      </div>
    </section>

    <!-- 侧边栏 token 示例 -->
    <section class="dt__section">
      <h2 class="dt__section-title">深色侧边栏</h2>
      <div class="dt__sidebar-demo">
        <div class="dt__sidebar-item dt__sidebar-item--active">
          模型管理（激活）
        </div>
        <div class="dt__sidebar-item">Agent 管理</div>
        <div class="dt__sidebar-item">对话</div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

interface TokenSwatch {
  step: string
  token: string
  hex: string
}

const primaryScale: TokenSwatch[] = [
  { step: '50', token: '--hf-primary-50', hex: '#F1F2FB' },
  { step: '100', token: '--hf-primary-100', hex: '#E4E7F9' },
  { step: '200', token: '--hf-primary-200', hex: '#CBCFF3' },
  { step: '300', token: '--hf-primary-300', hex: '#A8AEEA' },
  { step: '400', token: '--hf-primary-400', hex: '#8189DE' },
  { step: '500', token: '--hf-primary-500', hex: '#5E6AD2' },
  { step: '600', token: '--hf-primary-600', hex: '#4D57BE' },
  { step: '700', token: '--hf-primary-700', hex: '#40479C' },
  { step: '800', token: '--hf-primary-800', hex: '#363C7D' },
  { step: '900', token: '--hf-primary-900', hex: '#2F3364' },
  { step: '950', token: '--hf-primary-950', hex: '#1B1D3C' },
]

const accentScale: TokenSwatch[] = [
  { step: '50', token: '--hf-accent-50', hex: '#ECFEFF' },
  { step: '100', token: '--hf-accent-100', hex: '#CFFAFE' },
  { step: '200', token: '--hf-accent-200', hex: '#A5F3FC' },
  { step: '300', token: '--hf-accent-300', hex: '#67E8F9' },
  { step: '400', token: '--hf-accent-400', hex: '#22D3EE' },
  { step: '500', token: '--hf-accent-500', hex: '#06B6D4' },
  { step: '600', token: '--hf-accent-600', hex: '#0891B2' },
  { step: '700', token: '--hf-accent-700', hex: '#0E7490' },
  { step: '800', token: '--hf-accent-800', hex: '#155E75' },
  { step: '900', token: '--hf-accent-900', hex: '#164E63' },
]

interface SemanticColor {
  name: 'success' | 'warning' | 'danger' | 'info'
  label: string
  desc: string
}

const semantics: SemanticColor[] = [
  { name: 'success', label: '成功', desc: '连通成功 / 已完成 / 在线' },
  { name: 'warning', label: '警告', desc: '预算 80% / 降级 / 处理中' },
  { name: 'danger', label: '错误', desc: '失败 / 熔断 / 删除' },
  { name: 'info', label: '信息', desc: '中性提示 / 草稿' },
]

type ProviderStatus = 'online' | 'degraded' | 'down' | 'unconfigured'

interface ProviderRow {
  name: string
  kind: string
  status: ProviderStatus
  models: number
  tokensToday: string
}

const providerRows: ProviderRow[] = [
  { name: 'OpenAI 主力', kind: 'OpenAI', status: 'online', models: 12, tokensToday: '284,192' },
  { name: 'Claude 生产', kind: 'Anthropic', status: 'online', models: 5, tokensToday: '191,024' },
  { name: 'Gemini 实验', kind: 'Google', status: 'degraded', models: 8, tokensToday: '42,310' },
  { name: 'Ollama 本地', kind: 'Ollama', status: 'down', models: 3, tokensToday: '—' },
  { name: '备用 OpenAI', kind: 'OpenAI', status: 'unconfigured', models: 0, tokensToday: '—' },
]

const statusMeta: Record<
  ProviderStatus,
  { label: string; type: 'success' | 'warning' | 'danger' | 'info' }
> = {
  online: { label: '在线', type: 'success' },
  degraded: { label: '限流中', type: 'warning' },
  down: { label: '已熔断', type: 'danger' },
  unconfigured: { label: '未配置', type: 'info' },
}

const radii = [
  { token: '--hf-radius-xs', label: 'xs · 4' },
  { token: '--hf-radius-sm', label: 'sm · 6' },
  { token: '--hf-radius-md', label: 'md · 8' },
  { token: '--hf-radius-lg', label: 'lg · 12' },
  { token: '--hf-radius-xl', label: 'xl · 16' },
]

const shadows = [
  { token: '--hf-shadow-xs', label: 'xs' },
  { token: '--hf-shadow-sm', label: 'sm' },
  { token: '--hf-shadow-md', label: 'md' },
  { token: '--hf-shadow-lg', label: 'lg' },
  { token: '--hf-shadow-glow-primary', label: 'glow' },
  { token: '--hf-shadow-focus', label: 'focus' },
]

const fontSizes = [
  { token: '--hf-font-size-xs' },
  { token: '--hf-font-size-sm' },
  { token: '--hf-font-size-base' },
  { token: '--hf-font-size-md' },
  { token: '--hf-font-size-lg' },
  { token: '--hf-font-size-xl' },
  { token: '--hf-font-size-2xl' },
]

const moved = ref(false)
</script>

<style scoped>
.dt {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-6);
  max-width: 1080px;
}

/* ---------- 页头 ---------- */
.dt__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--hf-space-6);
}
.dt__title {
  margin: 0 0 var(--hf-space-2);
  font-size: var(--hf-font-size-2xl);
  font-weight: var(--hf-font-weight-semibold);
  line-height: var(--hf-leading-tight);
}
.dt__subtitle {
  margin: 0;
  color: var(--hf-text-2);
}
.dt__gradient {
  flex-shrink: 0;
  width: 180px;
  height: 56px;
  border-radius: var(--hf-radius-md);
  background: var(--hf-gradient-brand);
}

/* ---------- 区块容器 ---------- */
.dt__section {
  padding: var(--hf-space-6);
  background: var(--hf-bg-container);
  border: 1px solid var(--hf-border-2);
  border-radius: var(--hf-radius-md);
  box-shadow: var(--hf-shadow-xs);
}
.dt__section-title {
  margin: 0 0 var(--hf-space-4);
  font-size: var(--hf-font-size-lg);
  font-weight: var(--hf-font-weight-semibold);
}
.dt__section-title--sub {
  margin-top: var(--hf-space-6);
  font-size: var(--hf-font-size-md);
}

/* ---------- 色阶板 ---------- */
.dt__swatches {
  display: grid;
  grid-template-columns: repeat(11, 1fr);
  gap: var(--hf-space-2);
}
.dt__swatch-color {
  height: 48px;
  border-radius: var(--hf-radius-sm);
  border: 1px solid var(--hf-border-2);
}
.dt__swatch-meta {
  display: flex;
  flex-direction: column;
  margin-top: var(--hf-space-1);
  font-size: var(--hf-font-size-xs);
  color: var(--hf-text-2);
}
.dt__swatch-meta code {
  font-family: var(--hf-font-mono);
  color: var(--hf-text-3);
}

/* ---------- 语义色 ---------- */
.dt__semantic-list {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-3);
}
.dt__semantic {
  display: flex;
  align-items: center;
  gap: var(--hf-space-3);
}
.dt__semantic-solid {
  min-width: 88px;
  padding: 4px 12px;
  border-radius: var(--hf-radius-xs);
  background: var(--sem-base);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-sm);
  text-align: center;
}
.dt__semantic-soft {
  min-width: 88px;
  padding: 4px 12px;
  border-radius: var(--hf-radius-xs);
  background: var(--sem-soft);
  border: 1px solid var(--sem-border);
  color: var(--sem-base);
  font-size: var(--hf-font-size-sm);
  text-align: center;
}
.dt__semantic-desc {
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-sm);
}

/* ---------- 背景/文字/边框 ---------- */
.dt__layers {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--hf-space-4);
}
.dt__layer-page {
  padding: var(--hf-space-4);
  background: var(--hf-bg-page);
  border-radius: var(--hf-radius-md);
}
.dt__layer-container {
  margin-top: var(--hf-space-3);
  padding: var(--hf-space-4);
  background: var(--hf-bg-container);
  border: 1px solid var(--hf-border-1);
  border-radius: var(--hf-radius-sm);
}
.dt__layer-row {
  display: flex;
  gap: var(--hf-space-3);
  margin-top: var(--hf-space-3);
}
.dt__layer-subtle,
.dt__layer-muted {
  flex: 1;
  padding: var(--hf-space-3);
  border-radius: var(--hf-radius-xs);
}
.dt__layer-subtle {
  background: var(--hf-bg-subtle);
}
.dt__layer-muted {
  background: var(--hf-bg-muted);
}
.dt__layer-texts p {
  margin: 0 0 var(--hf-space-2);
}
.dt__link {
  color: var(--hf-text-link);
  text-decoration: none;
}
.dt__link:hover {
  text-decoration: underline;
}
.dt__border-row {
  display: flex;
  align-items: center;
  gap: var(--hf-space-3);
  margin-top: var(--hf-space-4);
}
.dt__border-line {
  flex: 1;
  height: 2px;
}
.dt__border-line--1 {
  background: var(--hf-border-1);
}
.dt__border-line--2 {
  background: var(--hf-border-2);
}
.dt__border-line--3 {
  background: var(--hf-border-3);
}

/* ---------- 按钮 ---------- */
.dt__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--hf-space-3);
  margin-bottom: var(--hf-space-4);
}
.dt__row:last-child {
  margin-bottom: 0;
}
.dt__btn-glow,
.dt__btn-gradient {
  height: 32px;
  padding: 0 16px;
  border: none;
  border-radius: var(--hf-radius-sm);
  color: var(--hf-text-inverse);
  font-size: var(--hf-font-size-base);
  cursor: pointer;
  transition:
    box-shadow var(--hf-duration-base) var(--hf-ease-in-out),
    background var(--hf-duration-base) var(--hf-ease-in-out);
}
.dt__btn-glow {
  background: var(--hf-primary-500);
}
.dt__btn-glow:hover {
  background: var(--hf-primary-600);
  box-shadow: var(--hf-shadow-glow-primary);
}
.dt__btn-gradient {
  background: var(--hf-gradient-brand);
}
.dt__btn-gradient:hover {
  background: var(--hf-gradient-brand-hover);
  box-shadow: var(--hf-shadow-glow-primary);
}

/* ---------- 表单 ---------- */
.dt__form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--hf-space-4);
  align-items: center;
}
.dt__slider {
  padding: 0 var(--hf-space-2);
}

/* ---------- 表格 ---------- */
.dt__pagination {
  display: flex;
  justify-content: flex-end;
  margin-top: var(--hf-space-4);
}
.dt__mono {
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-sm);
}

/* ---------- 圆角/阴影 ---------- */
.dt__tiles {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: var(--hf-space-4);
  margin-bottom: var(--hf-space-4);
}
.dt__tiles--shadow {
  margin-bottom: 0;
}
.dt__tile {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 64px;
  background: var(--hf-bg-subtle);
  color: var(--hf-text-2);
  font-size: var(--hf-font-size-sm);
}
.dt__tile--flat {
  background: var(--hf-bg-container);
}

/* ---------- 动效 ---------- */
.dt__motion {
  display: flex;
  flex-direction: column;
  gap: var(--hf-space-4);
}
.dt__motion-box {
  width: 160px;
  height: 48px;
  border: none;
  border-radius: var(--hf-radius-sm);
  background: var(--hf-primary-500);
  color: var(--hf-text-inverse);
  cursor: pointer;
  transition: transform var(--hf-duration-slow) var(--hf-ease-out);
}
.dt__motion-box--spring {
  background: var(--hf-accent-500);
  transition-timing-function: var(--hf-ease-spring);
}
.dt__motion-box--moved {
  transform: translateX(240px);
}
.dt__motion-note {
  margin: 0;
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-sm);
}

/* ---------- 字体 ---------- */
.dt__font-row {
  display: flex;
  align-items: baseline;
  gap: var(--hf-space-4);
  margin-bottom: var(--hf-space-2);
}
.dt__font-token {
  width: 180px;
  flex-shrink: 0;
  color: var(--hf-text-3);
  font-family: var(--hf-font-mono);
  font-size: var(--hf-font-size-xs);
}
.dt__font-mono {
  display: flex;
  gap: var(--hf-space-6);
  margin-top: var(--hf-space-4);
  padding: var(--hf-space-3) var(--hf-space-4);
  background: var(--hf-bg-subtle);
  border-radius: var(--hf-radius-sm);
}
.dt__font-mono code {
  font-family: var(--hf-font-mono);
  color: var(--hf-accent-600);
}

/* ---------- 侧边栏示例 ---------- */
.dt__sidebar-demo {
  width: 240px;
  padding: var(--hf-space-3);
  background: var(--hf-sidebar-bg);
  border-radius: var(--hf-radius-md);
}
.dt__sidebar-item {
  position: relative;
  padding: 10px var(--hf-space-3);
  margin-bottom: var(--hf-space-1);
  border-radius: var(--hf-radius-sm);
  color: var(--hf-sidebar-text);
  font-size: var(--hf-font-size-base);
  cursor: pointer;
  transition:
    background-color var(--hf-duration-fast) var(--hf-ease-in-out),
    color var(--hf-duration-fast) var(--hf-ease-in-out);
}
.dt__sidebar-item:hover {
  background: var(--hf-sidebar-bg-hover);
  color: var(--hf-sidebar-text-hover);
}
.dt__sidebar-item--active {
  background: var(--hf-sidebar-bg-active);
  color: var(--hf-sidebar-text-active);
}
/* 选中态左 3px 主色竖线（与 App.vue .el-menu-item.is-active::before 一致） */
.dt__sidebar-item--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  width: 3px;
  height: 60%;
  border-radius: var(--hf-radius-full);
  background-color: var(--hf-sidebar-indicator);
  transform: translateY(-50%);
}
</style>
