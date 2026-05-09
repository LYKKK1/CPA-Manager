import { create } from 'zustand';
import type {
  CodexInspectionAction,
  CodexInspectionLogLevel,
  CodexInspectionProgressSnapshot,
  CodexInspectionRunResult,
} from '@/features/monitoring/codexInspection';

export type CodexInspectionRunStatus = 'idle' | 'running' | 'paused' | 'success' | 'error';

export type CodexInspectionLogEntry = {
  id: string;
  level: CodexInspectionLogLevel;
  message: string;
  timestamp: number;
};

export type CodexInspectionExecutionTriggerSource = 'manual' | 'auto';

export type CodexInspectionHistoryEntry = {
  id: string;
  timestamp: number;
  fileName: string;
  account: string;
  action: CodexInspectionAction;
  reason: string;
  kind: 'issue' | 'execution';
  source: CodexInspectionExecutionTriggerSource | 'inspection' | 'timer';
  success?: boolean;
  error?: string;
};

type Updater<T> = T | ((previous: T) => T);

export const createIdleCodexInspectionProgress = (): CodexInspectionProgressSnapshot => ({
  total: 0,
  completed: 0,
  inFlight: 0,
  pending: 0,
  percent: 0,
  status: 'idle',
  summary: {
    totalFiles: 0,
    probeSetCount: 0,
    sampledCount: 0,
    deleteCount: 0,
    disableCount: 0,
    enableCount: 0,
    keepCount: 0,
  },
  startedAt: Date.now(),
  updatedAt: Date.now(),
});

const resolveUpdater = <T,>(updater: Updater<T>, previous: T): T =>
  typeof updater === 'function' ? (updater as (value: T) => T)(previous) : updater;

interface CodexInspectionStoreState {
  logs: CodexInspectionLogEntry[];
  logsCollapsed: boolean;
  runStatus: CodexInspectionRunStatus;
  progress: CodexInspectionProgressSnapshot;
  result: CodexInspectionRunResult | null;
  executing: boolean;
  historyEntries: CodexInspectionHistoryEntry[];
  runStartedAt: number | null;
  setLogs: (updater: Updater<CodexInspectionLogEntry[]>) => void;
  setLogsCollapsed: (value: boolean | ((previous: boolean) => boolean)) => void;
  setRunStatus: (value: CodexInspectionRunStatus) => void;
  setProgress: (value: CodexInspectionProgressSnapshot) => void;
  setResult: (value: CodexInspectionRunResult | null) => void;
  setExecuting: (value: boolean) => void;
  setHistoryEntries: (updater: Updater<CodexInspectionHistoryEntry[]>) => void;
  setRunStartedAt: (value: number | null) => void;
}

export const useCodexInspectionStore = create<CodexInspectionStoreState>((set) => ({
  logs: [],
  logsCollapsed: false,
  runStatus: 'idle',
  progress: createIdleCodexInspectionProgress(),
  result: null,
  executing: false,
  historyEntries: [],
  runStartedAt: null,
  setLogs: (updater) => set((state) => ({ logs: resolveUpdater(updater, state.logs) })),
  setLogsCollapsed: (value) =>
    set((state) => ({
      logsCollapsed: typeof value === 'function' ? (value as (previous: boolean) => boolean)(state.logsCollapsed) : value,
    })),
  setRunStatus: (runStatus) => set({ runStatus }),
  setProgress: (progress) => set({ progress }),
  setResult: (result) => set({ result }),
  setExecuting: (executing) => set({ executing }),
  setHistoryEntries: (updater) =>
    set((state) => ({ historyEntries: resolveUpdater(updater, state.historyEntries) })),
  setRunStartedAt: (runStartedAt) => set({ runStartedAt }),
}));

