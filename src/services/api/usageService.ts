import axios from 'axios';
import type { UsagePayload } from '@/features/monitoring/hooks/useUsageData';
import { normalizeApiBase } from '@/utils/connection';
import type { ModelPrice } from '@/utils/usage';

export interface UsageServiceInfo {
  service?: string;
  mode?: string;
  startedAt?: number;
}

export interface UsageServiceCollectorStatus {
  collector?: string;
  upstream?: string;
  queue?: string;
  lastConsumedAt?: number;
  lastInsertedAt?: number;
  totalInserted?: number;
  totalSkipped?: number;
  deadLetters?: number;
  lastError?: string;
}

export interface UsageServiceStatus {
  service?: string;
  dbPath?: string;
  events?: number;
  deadLetters?: number;
  collector?: UsageServiceCollectorStatus;
}

export interface UsageServiceSetupRequest {
  cpaBaseUrl: string;
  managementKey: string;
  queue?: string;
  popSide?: string;
}

export interface ModelPricesResponse {
  prices: Record<string, ModelPrice>;
}

export interface ModelPriceSyncResponse extends ModelPricesResponse {
  source?: string;
  imported: number;
  skipped: number;
}

export interface CodexInspectionScheduleConfig {
  enabled: boolean;
  intervalMinutes: number;
  autoToggle: boolean;
}

export interface CodexInspectionServerSummary {
  totalFiles: number;
  probeSetCount: number;
  sampledCount: number;
  deleteCount: number;
  disableCount: number;
  enableCount: number;
  keepCount: number;
  autoDisabled: number;
  autoEnabled: number;
  autoFailed: number;
  skippedDeletes: number;
  messages?: string[];
}

export interface CodexInspectionServerRun {
  summary: CodexInspectionServerSummary;
  startedAt: number;
  finishedAt: number;
  error?: string;
}

export interface CodexInspectionServerStatus {
  schedule: CodexInspectionScheduleConfig;
  running: boolean;
  lastRunAt?: number;
  nextRunAt?: number;
  lastError?: string;
  lastRun?: CodexInspectionServerRun;
}

export interface CodexInspectionScheduleSaveResponse {
  schedule: CodexInspectionScheduleConfig;
  status: CodexInspectionServerStatus;
}

const USAGE_SERVICE_TIMEOUT_MS = 15 * 1000;
export const USAGE_SERVICE_ID = 'cpa-manager';
export const LEGACY_USAGE_SERVICE_ID = 'cpa-usage-service';
export const USAGE_SERVICE_LAST_CPA_BASE_KEY = 'cpa-manager:last-cpa-base';
export const LEGACY_USAGE_SERVICE_LAST_CPA_BASE_KEY = 'cpa-usage-service:last-cpa-base';

export const isUsageServiceId = (service?: string): boolean =>
  service === USAGE_SERVICE_ID || service === LEGACY_USAGE_SERVICE_ID;

export const normalizeUsageServiceBase = (input: string): string => normalizeApiBase(input);

const buildUrl = (base: string, path: string): string => {
  const normalized = normalizeUsageServiceBase(base).replace(/\/+$/, '');
  return `${normalized}${path}`;
};

const authHeaders = (managementKey?: string) =>
  managementKey ? { Authorization: `Bearer ${managementKey}` } : undefined;

export const usageServiceApi = {
  getInfo: async (base: string): Promise<UsageServiceInfo> => {
    const response = await axios.get<UsageServiceInfo>(buildUrl(base, '/usage-service/info'), {
      timeout: USAGE_SERVICE_TIMEOUT_MS,
    });
    return response.data;
  },

  setup: async (base: string, payload: UsageServiceSetupRequest): Promise<void> => {
    await axios.post(buildUrl(base, '/setup'), payload, {
      timeout: USAGE_SERVICE_TIMEOUT_MS,
    });
  },

  getStatus: async (base: string, managementKey?: string): Promise<UsageServiceStatus> => {
    const response = await axios.get<UsageServiceStatus>(buildUrl(base, '/status'), {
      timeout: USAGE_SERVICE_TIMEOUT_MS,
      headers: authHeaders(managementKey),
    });
    return response.data;
  },

  getUsage: async (base: string, managementKey?: string): Promise<UsagePayload> => {
    const response = await axios.get<UsagePayload>(buildUrl(base, '/v0/management/usage'), {
      timeout: USAGE_SERVICE_TIMEOUT_MS,
      headers: authHeaders(managementKey),
    });
    return response.data;
  },

  getModelPrices: async (
    base: string,
    managementKey?: string
  ): Promise<ModelPricesResponse> => {
    const response = await axios.get<ModelPricesResponse>(
      buildUrl(base, '/v0/management/model-prices'),
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      }
    );
    return response.data;
  },

  saveModelPrices: async (
    base: string,
    prices: Record<string, ModelPrice>,
    managementKey?: string
  ): Promise<ModelPricesResponse> => {
    const response = await axios.put<ModelPricesResponse>(
      buildUrl(base, '/v0/management/model-prices'),
      { prices },
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      }
    );
    return response.data;
  },

  syncModelPrices: async (
    base: string,
    managementKey?: string,
    models?: string[]
  ): Promise<ModelPriceSyncResponse> => {
    const response = await axios.post<ModelPriceSyncResponse>(
      buildUrl(base, '/v0/management/model-prices/sync'),
      models ? { models } : {},
      {
        timeout: 30 * 1000,
        headers: authHeaders(managementKey),
      }
    );
    return response.data;
  },

  getCodexInspectionStatus: async (
    base: string,
    managementKey?: string
  ): Promise<CodexInspectionServerStatus> => {
    const response = await axios.get<CodexInspectionServerStatus>(
      buildUrl(base, '/v0/management/codex-inspection/schedule'),
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      }
    );
    return response.data;
  },

  saveCodexInspectionSchedule: async (
    base: string,
    schedule: CodexInspectionScheduleConfig,
    managementKey?: string
  ): Promise<CodexInspectionScheduleSaveResponse> => {
    const response = await axios.put<CodexInspectionScheduleSaveResponse>(
      buildUrl(base, '/v0/management/codex-inspection/schedule'),
      schedule,
      {
        timeout: USAGE_SERVICE_TIMEOUT_MS,
        headers: authHeaders(managementKey),
      }
    );
    return response.data;
  },
};
