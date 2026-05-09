import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import type { AuthFileItem } from '@/types';
import {
  AUTH_FILE_REFRESH_LEAD_MS,
  getAuthFileExpiryMs,
  getAuthFileLastRefreshMs,
  getAuthFileNextRefreshAfterMs,
  isRuntimeOnlyAuthFile,
  normalizeProviderKey,
} from '@/features/authFiles/constants';
import styles from '@/pages/AuthFilesPage.module.scss';

type RefreshRisk = 'expired' | 'due' | 'disabled' | 'unknown' | 'ok';

type RefreshPanelItem = {
  file: AuthFileItem;
  expiryMs: number | null;
  lastRefreshMs: number | null;
  nextRefreshMs: number | null;
  risk: RefreshRisk;
  sortScore: number;
};

type AuthFileRefreshPanelProps = {
  files: AuthFileItem[];
  disableControls: boolean;
  statusUpdating: Record<string, boolean>;
  tokenRefreshing?: Record<string, boolean>;
  detailsLoading?: boolean;
  onToggleStatus: (file: AuthFileItem, enabled: boolean) => void;
  onRefreshToken?: (file: AuthFileItem) => void;
  onRefreshFiles: () => void;
};

const formatDistance = (milliseconds: number) => {
  const abs = Math.abs(milliseconds);
  const totalMinutes = Math.max(1, Math.round(abs / 60_000));
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return hours > 0 ? `${days}天 ${hours}小时` : `${days}天`;
  if (hours > 0) return minutes > 0 ? `${hours}小时 ${minutes}分钟` : `${hours}小时`;
  return `${minutes}分钟`;
};

const formatDateTime = (value: number | null, locale: string) =>
  value ? new Date(value).toLocaleString(locale) : '--';

const getRefreshRisk = (file: AuthFileItem, expiryMs: number | null, nextRefreshMs: number | null): RefreshRisk => {
  if (file.disabled) return 'disabled';
  const now = Date.now();
  if (expiryMs && expiryMs <= now) return 'expired';
  if (nextRefreshMs && nextRefreshMs <= now) return 'due';
  if (expiryMs) {
    const provider = normalizeProviderKey(String(file.provider ?? file.type ?? ''));
    const lead = AUTH_FILE_REFRESH_LEAD_MS[provider] ?? 24 * 60 * 60 * 1000;
    if (expiryMs - now <= Math.max(lead, 24 * 60 * 60 * 1000)) return 'due';
    return 'ok';
  }
  return 'unknown';
};

export function AuthFileRefreshPanel({
  files,
  disableControls,
  statusUpdating,
  tokenRefreshing = {},
  detailsLoading = false,
  onToggleStatus,
  onRefreshToken,
  onRefreshFiles,
}: AuthFileRefreshPanelProps) {
  const { t, i18n } = useTranslation();

  const items = useMemo<RefreshPanelItem[]>(() => {
    return files
      .filter((file) => !isRuntimeOnlyAuthFile(file))
      .map((file) => {
        const expiryMs = getAuthFileExpiryMs(file);
        const lastRefreshMs = getAuthFileLastRefreshMs(file);
        const nextRefreshMs = getAuthFileNextRefreshAfterMs(file);
        const risk = getRefreshRisk(file, expiryMs, nextRefreshMs);
        const score = risk === 'expired' ? 0 : risk === 'disabled' ? 1 : risk === 'due' ? 2 : risk === 'unknown' ? 3 : 4;
        return { file, expiryMs, lastRefreshMs, nextRefreshMs, risk, sortScore: score };
      })
      .sort((left, right) => {
        if (left.sortScore !== right.sortScore) return left.sortScore - right.sortScore;
        return (left.expiryMs ?? Number.MAX_SAFE_INTEGER) - (right.expiryMs ?? Number.MAX_SAFE_INTEGER);
      });
  }, [files]);

  const disabledCount = items.filter((item) => item.file.disabled).length;
  const dueCount = items.filter((item) => item.risk === 'due' || item.risk === 'expired').length;
  const unknownCount = items.filter((item) => item.risk === 'unknown').length;
  const highlightedItems = items.filter((item) => item.risk !== 'ok').slice(0, 6);

  if (items.length === 0) return null;

  return (
    <div className={styles.refreshPanel}>
      <div className={styles.refreshPanelHeader}>
        <div>
          <h3>{t('auth_files.refresh_panel_title')}</h3>
          <p>{t('auth_files.refresh_panel_desc')}</p>
        </div>
        <Button variant="secondary" size="sm" onClick={onRefreshFiles} disabled={disableControls} loading={detailsLoading}>
          {t('auth_files.refresh_files')}
        </Button>
      </div>

      <div className={styles.refreshPanelHint}>{t('auth_files.refresh_panel_detail_hint')}</div>

      <div className={styles.refreshPanelStats}>
        <span>{t('auth_files.refresh_panel_total', { count: items.length })}</span>
        <span>{t('auth_files.refresh_panel_disabled', { count: disabledCount })}</span>
        <span>{t('auth_files.refresh_panel_due', { count: dueCount })}</span>
        <span>{t('auth_files.refresh_panel_unknown', { count: unknownCount })}</span>
      </div>

      {highlightedItems.length > 0 ? (
        <div className={styles.refreshPanelList}>
          {highlightedItems.map((item) => {
            const now = Date.now();
            const expiryDistance = item.expiryMs ? item.expiryMs - now : null;
            const riskLabel =
              item.risk === 'expired'
                ? t('auth_files.refresh_panel_status_expired')
                : item.risk === 'due'
                  ? t('auth_files.refresh_panel_status_due')
                  : item.risk === 'disabled'
                    ? t('auth_files.refresh_panel_status_disabled')
                    : t('auth_files.refresh_panel_status_unknown');

            return (
              <div key={item.file.name} className={styles.refreshPanelRow}>
                <div className={styles.refreshPanelMain}>
                  <strong title={item.file.name}>{item.file.name}</strong>
                  <span className={styles.refreshPanelMeta}>
                    {t('auth_files.refresh_panel_expiry', {
                      time: formatDateTime(item.expiryMs, i18n.language),
                      remaining: expiryDistance == null ? '--' : formatDistance(expiryDistance),
                    })}
                  </span>
                  <span className={styles.refreshPanelMeta}>
                    {t('auth_files.refresh_panel_last_refresh', {
                      time: formatDateTime(item.lastRefreshMs, i18n.language),
                    })}
                  </span>
                </div>
                <div className={styles.refreshPanelActions}>
                  <span className={`${styles.refreshPanelRisk} ${styles[`refreshPanelRisk_${item.risk}`]}`}>
                    {riskLabel}
                  </span>
                  {onRefreshToken ? (
                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => onRefreshToken(item.file)}
                      disabled={disableControls || tokenRefreshing[item.file.name] === true}
                      loading={tokenRefreshing[item.file.name] === true}
                    >
                      {t('auth_files.refresh_panel_refresh_now_button')}
                    </Button>
                  ) : null}
                  {item.file.disabled ? (
                    <Button
                      size="sm"
                      onClick={() => onToggleStatus(item.file, true)}
                      disabled={disableControls || statusUpdating[item.file.name] === true || tokenRefreshing[item.file.name] === true}
                      loading={statusUpdating[item.file.name] === true}
                    >
                      {t('auth_files.refresh_panel_enable_button')}
                    </Button>
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
      ) : (
        <div className={styles.refreshPanelEmpty}>{t('auth_files.refresh_panel_empty')}</div>
      )}
    </div>
  );
}
