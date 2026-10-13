import { useState, useEffect, useRef, Suspense, lazy } from 'react';
import type { ChangeEvent } from 'react';
import { Box, Typography, Card, CardContent, CircularProgress, Alert, Stack, Button, Dialog, DialogTitle, DialogContent, DialogActions } from '@mui/material';
import CloudUpload from '@mui/icons-material/CloudUpload';
import Delete from '@mui/icons-material/Delete';
import Photo from '@mui/icons-material/Photo';

import { useSdkTranslation } from '../hooks/useSdkTranslation';
import { BackendUrl, request } from '../utils/proxy';

const SkinViewer3D = lazy(() => import('../components/SkinViewer3D'));

type TextureType = 'skin' | 'cape';

function normalizeTextureUrl(url: string): string {
  if (!url) return url;
  try {
    const parsed = new URL(url);
    // @ts-ignore
    if (import.meta.env.DEV) return parsed.pathname;
    if (BackendUrl && BackendUrl.startsWith('http')) {
      const backendOrigin = new URL(BackendUrl).origin;
      return url.replace(parsed.origin, backendOrigin);
    }
    return parsed.pathname;
  } catch {
    return url;
  }
}

export default function TextureManagement() {
  const { t } = useSdkTranslation();
  const [skinUrl, setSkinUrl] = useState<string | null>(null);
  const [capeUrl, setCapeUrl] = useState<string | null>(null);
  const [skinLocalPreview, setSkinLocalPreview] = useState<string | null>(null);
  const [capeLocalPreview, setCapeLocalPreview] = useState<string | null>(null);
  const [skinFile, setSkinFile] = useState<File | null>(null);
  const [capeFile, setCapeFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState<TextureType | null>(null);
  const [deleting, setDeleting] = useState<TextureType | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<{ type: TextureType; action: 'upload' | 'delete' } | null>(null);
  const [confirmDeleteType, setConfirmDeleteType] = useState<TextureType | null>(null);
  const [loading, setLoading] = useState(true);

  const skinFileInputRef = useRef<HTMLInputElement>(null);
  const capeFileInputRef = useRef<HTMLInputElement>(null);

  const getCurrentProfileID = async (): Promise<string | null> => {
    const response = await request(`${BackendUrl}/texture/get`, { method: 'POST' });
    if (!response.success) {
      setError(response.message || t('common.serverError'));
      return null;
    }

    const profileID = response.data?.profiles?.[0]?.id;
    if (!profileID) {
      setError('Profile not found');
      return null;
    }

    return profileID;
  };

  const fetchTextures = async () => {
    try {
      const response = await request(`${BackendUrl}/texture/get`, { method: 'POST' });
      if (response.success && response.data) {
        // Assume for now we use the first profile or the one matching the current session
        const profile = response.data.profiles?.[0];
        if (profile) {
          const skinTexture = profile.textures?.skin;
          const capeTexture = profile.textures?.cape;
          const cacheBust = Date.now();
          setSkinUrl(skinTexture?.url ? `${normalizeTextureUrl(skinTexture.url)}?${cacheBust}` : null);
          setCapeUrl(capeTexture?.url ? `${normalizeTextureUrl(capeTexture.url)}?${cacheBust}` : null);
        }
      } else {
        setSkinUrl(null);
        setCapeUrl(null);
      }
    } catch {
      setSkinUrl(null);
      setCapeUrl(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTextures();
  }, []);

  const handleFileSelect = (event: ChangeEvent<HTMLInputElement>, type: TextureType) => {
    const file = event.target.files?.[0];
    if (!file) return;
    if (!file.type.startsWith('image/png')) {
      setError(t('profile.textureDialog.errors.pngOnly'));
      return;
    }
    if (file.size > 2 * 1024 * 1024) {
      setError(t('profile.textureDialog.errors.tooLarge'));
      return;
    }
    const img = new Image();
    const objectUrl = URL.createObjectURL(file);
    img.onload = () => {
      URL.revokeObjectURL(objectUrl);
      if (img.width > 8192 || img.height > 8192) {
        setError(t('profile.textureDialog.errors.tooWide'));
        return;
      }
      const reader = new FileReader();
      reader.onload = (e) => {
        const dataUrl = e.target?.result as string;
        if (type === 'skin') {
          setSkinLocalPreview(dataUrl);
          setSkinFile(file);
        } else {
          setCapeLocalPreview(dataUrl);
          setCapeFile(file);
        }
        setError(null);
      };
      reader.readAsDataURL(file);
    };
    img.onerror = () => {
      URL.revokeObjectURL(objectUrl);
      setError(t('profile.textureDialog.errors.pngOnly'));
    };
    img.src = objectUrl;
  };

  const handleUpload = async (type: TextureType) => {
    const file = type === 'skin' ? skinFile : capeFile;
    if (!file) {
      setError(t('profile.textureDialog.errors.selectFile', { type: t(type === 'skin' ? 'profile.textureDialog.skin' : 'profile.textureDialog.cape') }));
      return;
    }

    const profileID = await getCurrentProfileID();
    if (!profileID) {
      return;
    }

    setUploading(type);
    setError(null);
    try {
      const formData = new FormData();
      formData.append('type', type);
      formData.append('file', file);
      const response = await request(`${BackendUrl}/texture/upload/${profileID}`, { method: 'POST', body: formData });
      if (response.success) {
        setSuccess({ type, action: 'upload' });
        if (type === 'skin') {
          setSkinFile(null);
          setSkinLocalPreview(null);
          if (skinFileInputRef.current) skinFileInputRef.current.value = '';
        } else {
          setCapeFile(null);
          setCapeLocalPreview(null);
          if (capeFileInputRef.current) capeFileInputRef.current.value = '';
        }
        await fetchTextures();
      } else {
        setError(response.message || t('profile.textureDialog.errors.uploadFailed'));
      }
    } catch {
      setError(t('common.serverError'));
    } finally {
      setUploading(null);
    }
  };

  const handleDelete = async (type: TextureType) => {
    const profileID = await getCurrentProfileID();
    if (!profileID) {
      return;
    }

    setDeleting(type);
    setError(null);
    try {
      const response = await request(`${BackendUrl}/texture/delete/${profileID}/${type}`, {
        method: 'DELETE',
      });
      if (response.success) {
        setSuccess({ type, action: 'delete' });
        if (type === 'skin') setSkinUrl(null);
        else setCapeUrl(null);
        await fetchTextures();
      } else {
        setError(response.message || t('profile.textureDialog.errors.deleteFailed'));
      }
    } catch {
      setError(t('common.serverError'));
    } finally {
      setDeleting(null);
      setConfirmDeleteType(null);
    }
  };

  const skinPreviewUrl = skinLocalPreview || skinUrl;
  const capePreviewUrl = capeLocalPreview || capeUrl;
  const hasPreview = !!skinPreviewUrl || !!capePreviewUrl;
  const isActionLoading = uploading !== null || deleting !== null;

  const renderUploadSection = (type: TextureType) => {
    const label = t(type === 'skin' ? 'profile.textureDialog.skin' : 'profile.textureDialog.cape');
    const file = type === 'skin' ? skinFile : capeFile;
    const currentUrl = type === 'skin' ? skinUrl : capeUrl;
    const fileInputRef = type === 'skin' ? skinFileInputRef : capeFileInputRef;
    const isUploading = uploading === type;
    const isDeleting = deleting === type;

    return (
      <Box sx={{ p: 2, border: '1px solid', borderColor: 'divider', borderRadius: 1 }}>
        <Typography variant="subtitle1" sx={{ mb: 1.5, fontWeight: 600 }}>{label}</Typography>
        <input ref={fileInputRef} type="file" accept="image/png" onChange={(e) => handleFileSelect(e, type)} style={{ display: 'none' }} id={`${type}-upload`} />
        <Stack spacing={1.5}>
          <Stack direction="row" spacing={1}>
            <label htmlFor={`${type}-upload`} style={{ flex: 1 }}>
              <Button variant="outlined" component="span" startIcon={<CloudUpload />} fullWidth disabled={isActionLoading}>{t('profile.textureDialog.chooseFile')}</Button>
            </label>
            {file && (
              <Button variant="contained" onClick={() => handleUpload(type)} disabled={isActionLoading} sx={{ minWidth: 100 }}>
                {isUploading ? t('profile.textureDialog.uploading') : t('profile.textureDialog.upload')}
              </Button>
            )}
          </Stack>
          {currentUrl && (
            <Button variant="outlined" color="error" startIcon={<Delete />} onClick={() => setConfirmDeleteType(type)} disabled={isActionLoading}>
              {isDeleting ? t('profile.textureDialog.deleting') : t('profile.textureDialog.delete') + ' ' + label}
            </Button>
          )}
          {file && <Typography variant="caption" color="text.secondary">{t('profile.textureDialog.selected', { name: file.name })}</Typography>}
        </Stack>
      </Box>
    );
  };

  if (loading) return <Box sx={{ display: 'flex', justifyContent: 'center', py: 4 }}><CircularProgress /></Box>;

  return (
    <Box sx={{ py: 2 }}>
      <Typography variant="h5" gutterBottom>{t('profile.skinCapeTitle')}</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 3 }}>{t('profile.skinCapeSubtitle')}</Typography>

      {error && <Alert severity="error" sx={{ mb: 2 }} onClose={() => setError(null)}>{error}</Alert>}
      {success && !confirmDeleteType && (
        <Alert severity="success" sx={{ mb: 2 }}>
          {t(success.action === 'upload' ? 'profile.textureDialog.uploadedSuccess' : 'profile.textureDialog.deletedSuccess', {
            type: t(success.type === 'skin' ? 'profile.textureDialog.skin' : 'profile.textureDialog.cape'),
          })}
        </Alert>
      )}

      <Card>
        <CardContent>
          <Box sx={{ display: 'flex', gap: 4, flexDirection: { xs: 'column', md: 'row' } }}>
            <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
              <Typography variant="subtitle2" sx={{ mb: 2, alignSelf: 'flex-start' }}>{t('profile.textureDialog.preview')}</Typography>
              {hasPreview ? (
                <Box sx={{ position: 'relative', width: 200, height: 400 }}>
                  <Suspense fallback={<CircularProgress sx={{ position: 'absolute', top: '50%', left: '50%', transform: 'translate(-50%, -50%)' }} />}>
                    <SkinViewer3D skinUrl={skinPreviewUrl} capeUrl={capePreviewUrl} width={200} height={400} />
                  </Suspense>
                </Box>
              ) : (
                <Box sx={{ width: 200, height: 400, backgroundColor: 'grey.100', display: 'flex', alignItems: 'center', justifyContent: 'center', borderRadius: 1 }}>
                  <Photo sx={{ width: 48, height: 48, color: 'grey.400' }} />
                </Box>
              )}
            </Box>
            <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 2 }}>
              {renderUploadSection('skin')}
              {renderUploadSection('cape')}
            </Box>
          </Box>
        </CardContent>
      </Card>

      <Dialog open={!!confirmDeleteType} onClose={() => setConfirmDeleteType(null)} maxWidth="xs" fullWidth>
        <DialogTitle>{t('profile.textureDialog.title')}</DialogTitle>
        <DialogContent>
          <Alert severity="warning" sx={{ mt: 1 }}>{t('profile.textureDialog.deleteConfirm', { type: t(confirmDeleteType === 'skin' ? 'profile.textureDialog.skin' : 'profile.textureDialog.cape') })}</Alert>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmDeleteType(null)} disabled={isActionLoading}>{t('common.cancel')}</Button>
          <Button variant="contained" color="error" onClick={() => confirmDeleteType && handleDelete(confirmDeleteType)} disabled={isActionLoading}>
            {deleting ? t('profile.textureDialog.deleting') : t('common.confirm')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
