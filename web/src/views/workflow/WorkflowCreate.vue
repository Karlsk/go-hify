<template>
  <!-- 工作流创建第一步（spec 010 两步式重构，FR-012）：纯基础表单——名称 / 描述 /
       类型 + task 型 I/O Schema 表单行编辑（SchemaFieldsEditor，FR-013 第一步侧；
       chat 型不显示）。画布 / JSON 编排整体移至第二步（/workflows/create/orchestrate）。
       回填：hasForm 时从草稿 store 读回（「上一步内容保留」的第一步侧，FR-014）。
       「创建工作流」= 本地校验（name 必填 + task 型 schemaFieldsError，拦截不发
       请求）→ store.saveForm → 跳第二步；创建请求发生在第二步。 -->
  <div>
    <PageHeader
      title="新建工作流"
      description="第一步：填写基础信息；下一步进入整页编排（JSON / 拖拽）"
    >
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

        <!-- task 型独有：I/O Schema 行编辑（FR-013；chat 型不显示） -->
        <template v-if="form.type === 'task'">
          <el-form-item label="入参 Schema">
            <div class="workflow-create__schema">
              <SchemaFieldsEditor v-model="form.inputSchema" />
            </div>
          </el-form-item>
          <el-form-item label="出参 Schema">
            <div class="workflow-create__schema">
              <SchemaFieldsEditor v-model="form.outputSchema" />
            </div>
          </el-form-item>
        </template>

        <el-form-item>
          <el-button type="primary" @click="goOrchestrate">创建工作流</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft } from '@element-plus/icons-vue'
import type { FormInstance, FormRules } from 'element-plus'
import PageHeader from '@/components/PageHeader.vue'
import SchemaFieldsEditor from './SchemaFieldsEditor.vue'
import { notifyError } from '@/utils/notify'
import type { SchemaField, WorkflowType } from '@/api/workflow'
import { schemaFieldsError } from './graph'
import { useWorkflowCreateDraftStore } from '@/stores/workflowCreateDraft'

const router = useRouter()
const store = useWorkflowCreateDraftStore()
const formRef = ref<FormInstance>()

// ---- 表单（第一步纯基础信息；hasForm 时从草稿回填——FR-014 上一步内容保留） ----

const form = reactive({
  name: store.name,
  description: store.description,
  type: store.type,
  inputSchema: [...store.inputSchema],
  outputSchema: [...store.outputSchema],
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

// ---- 进入第二步：本地校验 → 草稿落盘 → 跳整页编排（不发创建请求） ----

async function goOrchestrate(): Promise<void> {
  const valid = await formRef.value?.validate().then(
    () => true,
    () => false,
  )
  if (!valid) return

  // task 型：Schema 字段行本地校验（SC-004 前端拦截，错误文案含行号）
  if (form.type === 'task') {
    const err =
      schemaFieldsError(form.inputSchema) ?? schemaFieldsError(form.outputSchema)
    if (err) {
      notifyError(err)
      return
    }
  }

  store.saveForm({
    name: form.name.trim(),
    description: form.description,
    type: form.type,
    inputSchema: form.inputSchema,
    outputSchema: form.outputSchema,
  })
  void router.push('/workflows/create/orchestrate')
}
</script>

<style scoped>
.workflow-create__form {
  /* 纯表单宽度（画布已移第二步，不再需要 1080 横向空间） */
  max-width: 720px;
}

/* 类型单选的行内灰字提示 */
.workflow-create__hint {
  margin-left: var(--hf-space-3);
  color: var(--hf-text-3);
  font-size: var(--hf-font-size-xs);
}

/* schema 编辑器占满标签右侧宽度 */
.workflow-create__schema {
  width: 100%;
}
</style>
