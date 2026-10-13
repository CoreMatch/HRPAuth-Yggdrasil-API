import { useState, useEffect, Suspense, lazy } from 'react';
import { Box, Typography, Alert, Switch, FormControlLabel, Stack, CircularProgress, Chip } from '@mui/material';
import ContentCopyIcon from '@mui/icons-material/ContentCopy';
import CheckIcon from '@mui/icons-material/Check';

import { useSdkTranslation } from '../hooks/useSdkTranslation';
import { enableMojangBind, disableMojangBind } from '../api/user';
import { mojangTextureUrl, fetchMojangProfile } from '../api/texture';
import { getAuthToken, getMbeEnabled, setMbeEnabled, BackendUrl, request, dataCache } from '../utils/proxy';

const SkinViewer3D = lazy(() => import('../components/SkinViewer3D'));

interface MojangProfile {
  id: string;
  name: string;
}

export interface MojangBindContentProps {
  hideHeader?: boolean;
}

export default function MojangBindContent({ hideHeader = false }: MojangBindContentProps) {
  const { t } = useSdkTranslation();
  const [mbeEnabled, setMbeEnabledState] = useState(false);
  const [mbeLoading, setMbeLoading] = useState(false);
  const [mbeError, setMbeError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [mojangUuid, setMojangUuid] = useState<string | null>(null);
  const [mojangProfile, setMojangProfile] = useState<MojangProfile | null>(null);
  const [hasCape, setHasCape] = useState(false);
  const [skinLoading, setSkinLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  const fetchMojangProfileLocal = async (uuid: string) => {
    setSkinLoading(true);
    try {
      const profile = await fetchMojangProfile(uuid);
      if (profile) {
        setMojangProfile({ id: profile.id, name: profile.name });
        setHasCape(profile.has_cape);
        dataCache.setMojangProfile(profile);
      } else {
        setMojangProfile(null);
      }
    } catch (err) {
      console.error('[MojangBind] fetchMojangProfileLocal error:', err);
      setMojangProfile(null);
    } finally {
      setSkinLoading(false);
    }
  };

  useEffect(() => {
    const fetchData = async () => {
      const token = getAuthToken();
      if (!token) {
        setMbeEnabledState(getMbeEnabled() ?? false);
        setLoading(false);
        return;
      }

      const cachedUser = dataCache.getUser();
      const cachedMojangProfile = dataCache.getMojangProfile();

      if (cachedUser) {
        const apiMbe = cachedUser.mbe;
        setMbeEnabledState(apiMbe !== undefined ? Boolean(apiMbe) : (getMbeEnabled() ?? false));
        const uuid = cachedUser.mojang_uuid;
        if (uuid) {
          setMojangUuid(uuid);
          if (cachedMojangProfile) {
            setMojangProfile({ id: cachedMojangProfile.id, name: cachedMojangProfile.name });
            setHasCape(cachedMojangProfile.has_cape);
          } else {
            fetchMojangProfileLocal(uuid);
          }
        }
        setLoading(false);
      }

      try {
        const resp = await request(`${BackendUrl}/user`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({}),
        });

        if (resp.success && resp.data) {
          dataCache.setUser(resp.data);
          const apiMbe = resp.data.mbe;
          setMbeEnabledState(apiMbe !== undefined ? Boolean(apiMbe) : (getMbeEnabled() ?? false));
          const uuid = resp.data.mojang_uuid;
          if (uuid) {
            setMojangUuid(uuid);
            fetchMojangProfileLocal(uuid);
          }
        } else {
          setMbeEnabledState(getMbeEnabled() ?? false);
        }
      } catch {
        setMbeEnabledState(getMbeEnabled() ?? false);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, []);

  const handleCopyUuid = async () => {
    if (!mojangUuid) return;
    await navigator.clipboard.writeText(mojangUuid);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  const handleToggleMbe = async () => {
    setMbeLoading(true);
    setMbeError(null);

    try {
      const resp = mbeEnabled
        ? await disableMojangBind()
        : await enableMojangBind();

      if (resp.success) {
        const newMbe = Boolean(resp.data?.mbe);
        setMbeEnabledState(newMbe);
        setMbeEnabled(newMbe);
      } else {
        setMbeError(resp.message || t('mojangBind.operationFailed'));
      }
    } catch {
      setMbeError(t('common.serverError'));
    } finally {
      setMbeLoading(false);
    }
  };

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
        <CircularProgress />
      </Box>
    );
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Box sx={{ flex: 1, mr: 2 }}>
          {!hideHeader && (
            <Typography variant="h6" gutterBottom>
              {t('mojangBind.title')}
            </Typography>
          )}
          <Typography variant="body2" color="text.secondary">
            {mbeEnabled
              ? t('mojangBind.subtitleEnabled')
              : t('mojangBind.subtitleDisabled')
            }
          </Typography>
          {mbeError && (
            <Alert severity="error" sx={{ mt: 1 }} onClose={() => setMbeError(null)}>
              {mbeError}
            </Alert>
          )}
        </Box>
        <FormControlLabel
          control={
            <Switch
              checked={mbeEnabled}
              onChange={handleToggleMbe}
              disabled={mbeLoading}
            />
          }
          label={mbeLoading ? '...' : mbeEnabled ? t('mojangBind.on') : t('mojangBind.off')}
        />
      </Box>

      {mojangUuid && (
        <Box>
          <Typography variant="h6" gutterBottom>
            {t('mojangBind.boundAccount')}
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2 }}>
            <Typography variant="body2" color="text.secondary">
              {t('mojangBind.uuidLabel')}
            </Typography>
            <Chip
              label={mojangUuid}
              size="small"
              sx={{ fontFamily: 'monospace' }}
            />
            <Box
              component="span"
              onClick={handleCopyUuid}
              sx={{ cursor: 'pointer', display: 'inline-flex', color: 'action.active' }}
            >
              {copied ? <CheckIcon fontSize="small" /> : <ContentCopyIcon fontSize="small" />}
            </Box>
          </Box>

          {skinLoading ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}>
              <CircularProgress />
            </Box>
          ) : mojangUuid ? (
            <Box sx={{ display: 'flex', gap: 3, flexWrap: 'wrap' }}>
              <Box>
                <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                  {t('mojangBind.skinPreview')}
                </Typography>
                <Suspense fallback={<CircularProgress />}>
                  <SkinViewer3D
                    skinUrl={mojangTextureUrl(mojangUuid, 'skin')}
                    capeUrl={mojangTextureUrl(mojangUuid, 'cape')}
                    width={200}
                    height={400}
                  />
                </Suspense>
              </Box>
              <Box sx={{ flex: 1, minWidth: 200 }}>
                <Typography variant="subtitle2" color="text.secondary" gutterBottom>
                  {t('mojangBind.playerInfo')}
                </Typography>
                <Stack spacing={1}>
                  <Box>
                    <Typography variant="caption" color="text.secondary">{t('mojangBind.name')}</Typography>
                    <Typography variant="body1">{mojangProfile?.name ?? '—'}</Typography>
                  </Box>
                  <Box>
                    <Typography variant="caption" color="text.secondary">{t('mojangBind.uuid')}</Typography>
                    <Typography variant="body1" sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
                      {mojangProfile?.id ?? mojangUuid}
                    </Typography>
                  </Box>
                  {hasCape && (
                    <Alert severity="info">{t('mojangBind.capeAvailable')}</Alert>
                  )}
                </Stack>
              </Box>
            </Box>
          ) : (
            <Alert severity="info">{t('mojangBind.noSkinData')}</Alert>
          )}
        </Box>
      )}
    </Box>
  );
}
