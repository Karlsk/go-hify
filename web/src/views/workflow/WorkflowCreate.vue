<template>
  <!-- 工作流创建页：表单（名称/描述/类型/task schema）+ 双模式配置区。
       数据源边界（FR-007）：type/name/description 与 input_schema/output_schema
       由本表单持有；JSON 编辑器文本只承载图配置（start_node_key/nodes/edges）。
       错误双轨：本地校验走 notifyError，请求失败由拦截器统一弹（页面不重复）。 -->
  <div>
    <PageHeader title="新建工作流" description="JSON 配置或可视化拖拽编排工作流节点">
      <template #actions>
        <el-button @click="router.push('/workflows')">
          <el-icon><ArrowLeft /></el-icon>
          返回列表
        </el-button>
      </template>
    </PageHeader>

    <el-card>
      <el-form
        ref="formRef"
        :model="form"
        :rules="rules"
        label-width="110px"
        class="workflow-create__form"
      >
        <el-form-item label="名称" prop="name">
          <el-input
            v-model="form.name"
            :maxlength="128"
            placeholder="如 客服分流"
          />
        </el-form-item>
        <el-form-item label="描述" prop="description">
          <el-input
            v-model="form.description"
            type="textarea"
            :rows="2"
            placeholder="用途说明（可选）"
          />
        </el-form-item>
        <el-form-item label="类型" prop="type">
          <el-radio-group v-model="form.type">
            <el-radio value="chat">对话型</el-radio>
            <el-radio value="task">任务型</el-radio>
          </el-radio-group>
          <span class="workflow-create__hint">
            对话型 = Agent 会话终答管道；任务型 = 可组合任务函数（可被子工作流引用）
          </span>
        </el-form-item>

        <!-- task 型独有：I/O schema 由表单区独立编辑（FR-006），不进图配置 JSON -->
        <template v-if="form.type === 'task'">
          <el-form-item label="入参 Schema">
            <div class="workflow-create__schema">
              <JsonConfigEditor
                v-model="inputSchemaText"
                :rows="4"
                placeholder='[{"name":"query","type":"string","required":true,"description":"查询词"}]（可选）'
              />
            </div>
          </el-form-item>
          <el-form-item label="出参 Schema">
            <div class="workflow-create__schema">
              <JsonConfigEditor
                v-model="outputSchemaText"
                :rows="4"
                placeholder='[{"name":"reply","type":"string","required":true}]（可选）'
              />
            </div>
          </el-form-item>
        </template>

        <el-form-item label="配置模式">
          <el-radio-group v-model="mode" @change="onModeChange">
            <el-radio-button value="json">JSON</el-radio-button>
            <el-radio-button value="canvas">拖拽</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <el-form-item label="工作流配置">
          <template v-if="mode === 'json'">
            <JsonConfigEditor
              v-model="graphText"
              :rows="14"
              class="workflow-create__graph"
            />
          </template>
          <div v-else class="workflow-create__canvas">
            <CanvasEditor ref="canvasRef" :config="canvasConfig" :positions="positions" />
          </div>
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            :loading="submitting"
            @click="submit"
          >
            创建工作流
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft } from '@element-plus/icons-vue'
import type { FormInstance, FormRules } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import JsonConfigEditor from './JsonConfigEditor.vue'
import CanvasEditor from './CanvasEditor.vue'
import { notifyError, notifySuccess } from '@/utils/notify'
import { createWorkflow, type SchemaField, type WorkflowType } from '@/api/workflow'
import {
  PREFILL_GRAPH,
  buildCreatePayload,
  graphSubmitError,
  parseGraphConfig,
  parseSchemaFields,
  serializeGraphConfig,
  type GraphConfig,
  type NodePositions,
} from './graph'

const router = useRouter()
const formRef = ref<FormInstance>()

// ---- 表单（type/name/description 由表单持有，JSON 编辑器不含——FR-005） ----

const form = reactive({
  name: '',
  description: '',
  type: 'chat' as WorkflowType,
})

const rules: FormRules = {
  name: [
    {
      required: true,
      validator: (_rule, value: string, callback) => {
        if (!value || !value.trim()) callback(new Error('请输入名称'))
        else callback()
      },
      trigger: 'blur',
    },
  ],
}

// ---- 图配置双模式（FR-007/SC-006）：JSON 文本预填智能客服分类示例（手测文档 §4） ----
// 双模式共享单一图配置数据源：切画布 = JSON 解析进 canvasConfig（空配置 = 空画布起步，
// 不预置节点）；切回 JSON = getGraph() 重新序列化更新编辑器——往返语义等价、未知键透传。
// 节点位置存 positions Map（会话级持有，往返保留、不序列化进配置——research #6）。

const mode = ref<'json' | 'canvas'>('json')
const graphText = ref(serializeGraphConfig(PREFILL_GRAPH))

/** 画布实例（getGraph 出口转换；v-if 挂载期间可用） */
const canvasRef = ref<{ getGraph: () => GraphConfig }>()
/** 切画布时的初始图配置（切回 JSON 后下次切入重新解析赋值） */
const canvasConfig = ref<GraphConfig>({ start_node_key: '', nodes: [], edges: [] })
/** 画布节点位置（会话级；子组件经 props 引用读写） */
const positions = shallowRef<NodePositions>(new Map())

// task 型 schema 文本（空 = 不携带该键）
const inputSchemaText = ref('')
const outputSchemaText = ref('')

const submitting = ref(false)

// ---- 模式切换：非法 JSON 阻断切拖拽并提示先修复（spec US3 S5） ----

/**
 * 切画布 = JSON 解析进配置模型（画布按当前配置渲染，空配置即空画布）；
 * 切回 JSON = 画布编辑结果序列化回编辑器。el-radio 的 change 在 v-model 更新后
 * 同步触发、先于 v-if 重渲染——此刻画布 ref 仍可读。
 */
function onModeChange(value: string | number | boolean | undefined): void {
  if (value === 'canvas') {
    const r = parseGraphConfig(graphText.value)
    if (!r.ok) {
      mode.value = 'json' // 非法 JSON：回退本次切换，留在 JSON 模式修复
      notifyError(`JSON 非法，请先修复后再切换到拖拽模式：${r.error}`)
      return
    }
    canvasConfig.value = r.config
    return
  }
  if (value === 'json') {
    const g = canvasRef.value?.getGraph()
    if (g) graphText.value = serializeGraphConfig(g)
  }
}

// ---- 提交（FR-011：config 外键字符串保形零转换；chat 型不带 schema 键） ----

/** 当前图配置：JSON 模式解析文本；画布模式经 getGraph 出口转换（画布即数据源） */
function currentGraph(): GraphConfig | null {
  if (mode.value === 'json') {
    const r = parseGraphConfig(graphText.value)
    if (!r.ok) {
      notifyError(`工作流配置 JSON 非法：${r.error}`)
      return null
    }
    return r.config
  }
  const g = canvasRef.value?.getGraph()
  if (!g) {
    notifyError('画布未就绪，请稍后重试')
    return null
  }
  return g
}

async function submit(): Promise<void> {
  const valid = await formRef.value?.validate().then(
    () => true,
    () => false,
  )
  if (!valid) return

  const graph = currentGraph()
  if (!graph) return

  let inputSchema: SchemaField[] | undefined
  let outputSchema: SchemaField[] | undefined
  if (form.type === 'task') {
    const ri = parseSchemaFields(inputSchemaText.value)
    if (!ri.ok) {
      notifyError(`input_schema 非法：${ri.error}`)
      return
    }
    const ro = parseSchemaFields(outputSchemaText.value)
    if (!ro.ok) {
      notifyError(`output_schema 非法：${ro.error}`)
      return
    }
    inputSchema = ri.fields.length ? ri.fields : undefined
    outputSchema = ro.fields.length ? ro.fields : undefined
  }

  const graphError = graphSubmitError(graph)
  if (graphError) {
    notifyError(graphError)
    return
  }

  submitting.value = true
  try {
    await createWorkflow(
      buildCreatePayload({
        name: form.name,
        description: form.description,
        type: form.type,
        graph,
        inputSchema,
        outputSchema,
      }),
    )
  } catch {
    return // 失败提示已由拦截器弹；留在当前页（编辑内容不丢）
  } finally {
    submitting.value = false
  }
  notifySuccess('创建成功')
  void router.push('/workflows')
}
</script>

<style scoped>
.workflow-create__form {
  /* 1080：画布模式需要横向空间（140 面板 + 画布 + 250 检查器）；JSON 编辑器同样受益 */
  max-width: 1080px;
}

/* 类型单选的行内灰字提示 */
.workflow-create__hint {
  margin-left: var(--hf-space-3);
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
}

/* schema / 图配置编辑器占满标签右侧宽度 */
.workflow-create__schema,
.workflow-create__graph {
  width: 100%;
}

.workflow-create__canvas {
  width: 100%;
}
</style>
