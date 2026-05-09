import { authFilesApi } from '@/services/api';
import type { AuthFileItem } from '@/types';
import { isRuntimeOnlyAuthFile, normalizeProviderKey } from './constants';

const CODEX_REFRESH_URL = 'https://auth.openai.com/oauth/token';
const CODEX_CLIENT_ID = 'app_EMoamEEZ73f0CkXaXp7hrann';
const CODEX_REDIRECT_URI = 'http://localhost:1455/auth/callback';

export const CODEX_TOKEN_SOON_MS = 2 * 24 * 60 * 60 * 1000;

export const isCodexAuthFile = (file: AuthFileItem): boolean => {
  const provider = normalizeProviderKey(String(file.provider ?? file.type ?? ''));
  return provider === 'codex' || file.name.toLowerCase().includes('codex');
};

export const mergeAuthFileDetail = (file: AuthFileItem, detail: Record<string, unknown>): AuthFileItem => ({
  ...file,
  ...detail,
  name: file.name,
  type: file.type ?? (detail.type as AuthFileItem['type']),
  provider: file.provider ?? (detail.provider as string | undefined),
  disabled: file.disabled,
});

export const loadCodexAuthFileDetails = async (files: AuthFileItem[]): Promise<AuthFileItem[]> => {
  const codexFiles = files.filter((file) => isCodexAuthFile(file) && !isRuntimeOnlyAuthFile(file));
  const detailPairs = await Promise.all(
    codexFiles.map(async (file) => {
      try {
        const detail = await authFilesApi.downloadJsonObject(file.name);
        return [file.name, mergeAuthFileDetail(file, detail)] as const;
      } catch {
        return [file.name, file] as const;
      }
    })
  );
  const detailMap = new Map(detailPairs);
  return files.map((file) => detailMap.get(file.name) ?? file);
};

const readStringField = (record: Record<string, unknown>, ...keys: string[]): string => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
  }
  return '';
};

export const refreshCodexTokenDetail = async (detail: Record<string, unknown>): Promise<Record<string, unknown>> => {
  const refreshToken = readStringField(detail, 'refresh_token', 'refreshToken');
  if (!refreshToken) throw new Error('凭证详情缺少 refresh_token，无法立即刷新');

  const response = await fetch(CODEX_REFRESH_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      redirect_uri: CODEX_REDIRECT_URI,
      grant_type: 'refresh_token',
      client_id: CODEX_CLIENT_ID,
      refresh_token: refreshToken,
    }),
  });
  const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
  if (!response.ok) {
    const error = payload.error;
    const message =
      error && typeof error === 'object' && typeof (error as Record<string, unknown>).message === 'string'
        ? String((error as Record<string, unknown>).message)
        : typeof payload.error_description === 'string'
          ? payload.error_description
          : `HTTP ${response.status}`;
    throw new Error(message);
  }

  const accessToken = readStringField(payload, 'access_token', 'accessToken');
  if (!accessToken) throw new Error('刷新响应缺少 access_token');
  const nextRefreshToken = readStringField(payload, 'refresh_token', 'refreshToken') || refreshToken;
  const expiresIn = Number(payload.expires_in ?? payload.expiresIn ?? 864_000);
  const now = new Date();
  const expired = new Date(now.getTime() + (Number.isFinite(expiresIn) ? expiresIn : 864_000) * 1000);

  return {
    ...detail,
    access_token: accessToken,
    refresh_token: nextRefreshToken,
    id_token: payload.id_token ?? payload.idToken ?? detail.id_token ?? detail.idToken,
    last_refresh: now.toISOString(),
    expired: expired.toISOString(),
  };
};

