/**
 * 工作流创建草稿 store（Pinia，spec 010 US4）：两步式创建的第一步 ↔ 第二步
 * 数据传递（第一步表单 / 第二步图编排互访保留，FR-014）。
 *
 * 生命周期 = 内存态：刷新 / 直访第二步时 state 归零 → hasForm false → 第二步
 * 挂载守卫回第一步（「直访 / 刷新回第一步」语义天然成立，不落 localStorage——
 * 内部工具，创建草稿不值得持久化，research #1 / data-model §3）。
 */
import { defineStore } from 'pinia'
import type { SchemaField, WorkflowType } from '@/api/workflow'
import type { GraphConfig } from '@/views/workflow/graph'

/** 第一步表单快照（saveForm 整体写入） */
export interface WorkflowFormDraft {
  name: string
  description: string
  type: WorkflowType
  inputSchema: SchemaField[]
  outputSchema: SchemaField[]
}

export const useWorkflowCreateDraftStore = defineStore('workflowCreateDraft', {
  state: () => ({
    name: '',
    description: '',
    type: 'chat' as WorkflowType,
    inputSchema: [] as SchemaField[],
    outputSchema: [] as SchemaField[],
    /** 第二步图编排（null = 尚未进过第二步，初始用 PREFILL 深拷贝） */
    graph: null as GraphConfig | null,
  }),
  getters: {
    /** 第一步已填过（name 非空）：false → 第二步挂载守卫回第一步 */
    hasForm: (state) => state.name !== '',
  },
  actions: {
    /** 第一步「创建工作流」：表单整体写入（含 task 型 schema），随后跳第二步 */
    saveForm(form: WorkflowFormDraft) {
      this.name = form.name
      this.description = form.description
      this.type = form.type
      this.inputSchema = form.inputSchema
      this.outputSchema = form.outputSchema
    },
    /** 第二步离开（上一步 / 侧边栏误点）：图编排回写 */
    saveGraph(graph: GraphConfig) {
      this.graph = graph
    },
    /** 创建成功：草稿清零 */
    clear() {
      this.name = ''
      this.description = ''
      this.type = 'chat'
      this.inputSchema = []
      this.outputSchema = []
      this.graph = null
    },
  },
})
