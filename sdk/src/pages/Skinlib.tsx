import React, { useState, useEffect } from 'react';
import {
  Box,
  Typography,
  Grid,
  Card,
  CardContent,
  CardActions,
  CardMedia,
  CircularProgress,
  Alert,
  Snackbar,
  FormControl,
  InputLabel,
  Select,
  MenuItem,
  Pagination,
  Stack,
  Chip,
  TextField,
  InputAdornment,
  Button,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  RadioGroup,
  FormControlLabel,
  Radio,
  FormLabel,
  Checkbox,
} from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import CloudUploadIcon from '@mui/icons-material/CloudUpload';
import SettingsInputComponentIcon from '@mui/icons-material/SettingsInputComponent';
import DeleteIcon from '@mui/icons-material/Delete';
import { useSdkTranslation } from '../hooks/useSdkTranslation';
import { listTextures, getPreviewUrl, uploadTexture, pullTexture, applyTextureToUser, deleteTexture } from '../api/texture';
import type { TextureItem, TextureListRequest, TextureType, SkinModel } from '../types/texture';
import { getAuthToken, getUid, SkinlibUrl, setSkinlibUrl } from '../utils/proxy';

interface UploadDialogProps {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
}

const UploadDialog: React.FC<UploadDialogProps> = ({ open, onClose, onSuccess }) => {
  const { t } = useSdkTranslation();
  const [file, setFile] = useState<File | null>(null);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [type, setType] = useState<TextureType>('skin');
  const [model, setModel] = useState<SkinModel>('default');
  const [tags, setTags] = useState('');
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [securityConfirmed, setSecurityConfirmed] = useState(false);

  const isCustomUrl = !!SkinlibUrl;

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files[0]) {
      const selectedFile = e.target.files[0];
      if (selectedFile.type !== 'image/png') {
        setError(t('skinlib.uploadDialog.pngOnly'));
        return;
      }
      if (selectedFile.size > 2 * 1024 * 1024) {
        setError(t('skinlib.uploadDialog.errors.tooLarge'));
        return;
      }
      const img = new Image();
      const objectUrl = URL.createObjectURL(selectedFile);
      img.onload = () => {
        URL.revokeObjectURL(objectUrl);
        if (img.width > 8192 || img.height > 8192) {
          setError(t('skinlib.uploadDialog.errors.tooWide'));
          return;
        }
        setFile(selectedFile);
        setError(null);
        if (!name) {
          setName(selectedFile.name.replace(/\.[^/.]+$/, ""));
        }
      };
      img.onerror = () => {
        URL.revokeObjectURL(objectUrl);
        setError(t('skinlib.uploadDialog.pngOnly'));
      };
      img.src = objectUrl;
    }
  };

  const handleUpload = async () => {
    if (!file || !name) {
      setError(t('skinlib.uploadDialog.selectFileAndName'));
      return;
    }

    if (isCustomUrl && !securityConfirmed) {
      setError(t('skinlib.uploadDialog.confirmSecurityFirst'));
      return;
    }

    const token = getAuthToken();
    const uidStr = getUid();
    const uid = uidStr ? parseInt(uidStr) : NaN;

    if (!token || !uidStr || isNaN(uid) || uid <= 0) {
      setError(t('skinlib.notLoggedInUid'));
      return;
    }

    setUploading(true);
    setError(null);

    try {
      const result = await uploadTexture({
        uid: uid,
        type,
        model: type === 'skin' ? model : undefined,
        name,
        description,
        tags,
        file,
      });

      if (result.success) {
        onSuccess();
        handleClose();
      } else {
        setError(result.message || t('skinlib.uploadDialog.uploadFailed'));
      }
    } catch (err) {
      setError(t('skinlib.uploadDialog.uploadError'));
    } finally {
      setUploading(false);
    }
  };

  const handleClose = () => {
    setFile(null);
    setName('');
    setDescription('');
    setType('skin');
    setModel('default');
    setTags('');
    setError(null);
    setSecurityConfirmed(false);
    onClose();
  };

  return (
    <Dialog open={open} onClose={handleClose} maxWidth="sm" fullWidth>
      <DialogTitle>{t('skinlib.uploadDialog.title')}</DialogTitle>
      <DialogContent>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 1 }}>
          {error && <Alert severity="error">{error}</Alert>}

          <Button
            variant="outlined"
            component="label"
            startIcon={<CloudUploadIcon />}
            fullWidth
            sx={{ py: 2 }}
          >
            {file ? t('skinlib.uploadDialog.selected', { name: file.name }) : t('skinlib.uploadDialog.selectFile')}
            <input type="file" hidden accept="image/png" onChange={handleFileChange} />
          </Button>

          <TextField
            label={t('skinlib.uploadDialog.nameLabel')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            fullWidth
            size="small"
          />

          <FormControl>
            <FormLabel>{t('skinlib.uploadDialog.typeLabel')}</FormLabel>
            <RadioGroup
              row
              value={type}
              onChange={(e) => setType(e.target.value as TextureType)}
            >
              <FormControlLabel value="skin" control={<Radio />} label={t('skinlib.uploadDialog.typeSkin')} />
              <FormControlLabel value="cape" control={<Radio />} label={t('skinlib.uploadDialog.typeCape')} />
            </RadioGroup>
          </FormControl>

          {type === 'skin' && (
            <FormControl>
              <FormLabel>{t('skinlib.uploadDialog.modelLabel')}</FormLabel>
              <RadioGroup
                row
                value={model}
                onChange={(e) => setModel(e.target.value as SkinModel)}
              >
                <FormControlLabel value="default" control={<Radio />} label={t('skinlib.uploadDialog.modelDefault')} />
                <FormControlLabel value="slim" control={<Radio />} label={t('skinlib.uploadDialog.modelSlim')} />
              </RadioGroup>
            </FormControl>
          )}

          <TextField
            label={t('skinlib.uploadDialog.descriptionLabel')}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            fullWidth
            multiline
            rows={2}
            size="small"
          />

          <TextField
            label={t('skinlib.uploadDialog.tagsLabel')}
            value={tags}
            onChange={(e) => setTags(e.target.value)}
            fullWidth
            size="small"
            placeholder={t('skinlib.uploadDialog.tagsPlaceholder')}
          />

          {isCustomUrl && (
            <Box sx={{ mt: 1, p: 2, border: '1px solid', borderColor: 'error.main', borderRadius: 1, bgcolor: 'rgba(211, 47, 47, 0.04)' }}>
              <FormControlLabel
                control={
                  <Checkbox
                    checked={securityConfirmed}
                    onChange={(e) => setSecurityConfirmed(e.target.checked)}
                    color="error"
                  />
                }
                label={
                  <Typography variant="body2" color="error.main" sx={{ fontWeight: 'bold' }}>
                    {t('skinlib.uploadDialog.securityConfirm')}
                  </Typography>
                }
              />
            </Box>
          )}
        </Box>
      </DialogContent>
      <DialogActions>
        <Button onClick={handleClose} disabled={uploading}>{t('common.cancel')}</Button>
        <Button
          onClick={handleUpload}
          variant="contained"
          disabled={uploading || !file || !name || (isCustomUrl && !securityConfirmed)}
          color={isCustomUrl ? "error" : "primary"}
        >
          {uploading ? t('skinlib.uploadDialog.uploading') : t('skinlib.uploadDialog.submit')}
        </Button>
      </DialogActions>
    </Dialog>
  );
};

const Skinlib: React.FC = () => {
  const { t } = useSdkTranslation();

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [textures, setTextures] = useState<TextureItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [type, setType] = useState<TextureListRequest['type']>('all');
  const [order, setOrder] = useState<TextureListRequest['order']>('desc');
  const [tag, setTag] = useState('');
  const [searchTag, setSearchTag] = useState('');
  const [uploadDialogOpen, setUploadDialogOpen] = useState(false);
  const [sourceUrl, setSourceUrl] = useState(SkinlibUrl);
  const [usingHash, setUsingHash] = useState<string | null>(null);
  const [deletingHash, setDeletingHash] = useState<string | null>(null);
  const [confirmDeleteItem, setConfirmDeleteItem] = useState<TextureItem | null>(null);
  const [snackbar, setSnackbar] = useState<{ open: boolean; message: string; severity: 'success' | 'error' }>({
    open: false,
    message: '',
    severity: 'success',
  });

  const fetchTextures = async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await listTextures({
        type,
        order,
        tag: searchTag,
        page
      });

      if (result.success && result.data) {
        setTextures(result.data.items);
        setTotal(result.data.total);
      } else {
        setError(result.message || t('skinlib.loadFailed'));
      }
    } catch (err) {
      setError(t('skinlib.networkError'));
    } finally {
      setLoading(false);
    }
  };

  const handleUseTexture = async (item: TextureItem) => {
    const token = getAuthToken();
    const uidStr = getUid();
    const uid = uidStr ? parseInt(uidStr) : NaN;

    if (!token || !uidStr || isNaN(uid) || uid <= 0) {
      setSnackbar({ open: true, message: t('skinlib.notLoggedInUid'), severity: 'error' });
      return;
    }

    setUsingHash(item.hash);

    try {
      // Step 1: Pull the skin source file from texture/pull API
      const blob = await pullTexture(item.hash);

      if ('success' in blob && !blob.success) {
        setSnackbar({ open: true, message: blob.message || t('skinlib.applyFailed'), severity: 'error' });
        return;
      }

      // Step 2: Upload to HA business system (equivalent to Profile.tsx logic)
      const file = new File([blob as Blob], item.file_name, { type: 'image/png' });

      const result = await applyTextureToUser(
        item.type,
        file,
        item.type === 'skin' ? (item.model || 'default') : undefined,
        uid
      );

      if (result.success) {
        setSnackbar({ open: true, message: t('skinlib.applySuccess', { name: item.name }), severity: 'success' });
      } else {
        setSnackbar({ open: true, message: result.message || t('skinlib.applyFailed'), severity: 'error' });
      }
    } catch (err) {
      setSnackbar({ open: true, message: t('skinlib.operationError'), severity: 'error' });
    } finally {
      setUsingHash(null);
    }
  };

  const handleDeleteTexture = async (item: TextureItem) => {
    const token = getAuthToken();
    if (!token) {
      setSnackbar({ open: true, message: t('skinlib.notLoggedIn'), severity: 'error' });
      return;
    }

    setDeletingHash(item.hash);

    try {
      const result = await deleteTexture({ type: item.type, hash: item.hash });

      if (result.success) {
        setSnackbar({ open: true, message: t('skinlib.deleteSuccess', { name: item.name }), severity: 'success' });
        setConfirmDeleteItem(null);
        fetchTextures();
      } else {
        setSnackbar({ open: true, message: result.message || t('skinlib.deleteFailed'), severity: 'error' });
      }
    } catch {
      setSnackbar({ open: true, message: t('skinlib.deleteError'), severity: 'error' });
    } finally {
      setDeletingHash(null);
    }
  };

  useEffect(() => {
    fetchTextures();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [type, order, searchTag, page, sourceUrl]);

  const handlePageChange = (_: React.ChangeEvent<unknown>, value: number) => {
    setPage(value);
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setSearchTag(tag);
    setPage(1);
  };

  return (
    <Box sx={{ py: 4 }}>
      <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', mb: 4, gap: 2, flexDirection: { xs: 'column', sm: 'row' } }}>
        <Box>
          <Typography variant="h4" component="h1" sx={{ fontWeight: 'bold' }}>
            {t('skinlib.title')}
          </Typography>
          <Typography variant="body2" color="text.secondary">
            {t('skinlib.currentSource', { url: sourceUrl || '(开发环境代理)' })}
          </Typography>
        </Box>

        <Stack direction="row" spacing={2} sx={{ width: { xs: '100%', sm: 'auto' } }}>
          <TextField
            size="small"
            label={t('skinlib.sourceLabel')}
            placeholder={t('skinlib.sourcePlaceholder')}
            value={sourceUrl}
            onChange={(e) => {
              const newUrl = e.target.value;
              setSourceUrl(newUrl);
              setSkinlibUrl(newUrl);
              setPage(1);
            }}
            slotProps={{
              input: {
                startAdornment: (
                  <InputAdornment position="start">
                    <SettingsInputComponentIcon fontSize="small" />
                  </InputAdornment>
                ),
              },
            }}
            sx={{ minWidth: 250 }}
          />
          <Button
            variant="contained"
            startIcon={<CloudUploadIcon />}
            onClick={() => setUploadDialogOpen(true)}
            sx={{ whiteSpace: 'nowrap' }}
          >
            {t('skinlib.upload')}
          </Button>
        </Stack>
      </Box>

      {/* 筛选栏 */}

      <Box sx={{ mb: 4, display: 'flex', gap: 2, flexWrap: 'wrap', alignItems: 'center' }}>
        <FormControl size="small" sx={{ minWidth: 120 }}>
          <InputLabel>{t('skinlib.typeFilter')}</InputLabel>
          <Select
            value={type}
            label={t('skinlib.typeFilter')}
            onChange={(e) => {
              setType(e.target.value as any);
              setPage(1);
            }}
          >
            <MenuItem value="all">{t('skinlib.typeAll')}</MenuItem>
            <MenuItem value="default">{t('skinlib.typeDefault')}</MenuItem>
            <MenuItem value="slim">{t('skinlib.typeSlim')}</MenuItem>
            <MenuItem value="cape">{t('skinlib.typeCape')}</MenuItem>
          </Select>
        </FormControl>

        <FormControl size="small" sx={{ minWidth: 120 }}>
          <InputLabel>{t('skinlib.sort')}</InputLabel>
          <Select
            value={order}
            label={t('skinlib.sort')}
            onChange={(e) => {
              setOrder(e.target.value as any);
              setPage(1);
            }}
          >
            <MenuItem value="desc">{t('skinlib.sortDesc')}</MenuItem>
            <MenuItem value="asc">{t('skinlib.sortAsc')}</MenuItem>
          </Select>
        </FormControl>

        <Box component="form" onSubmit={handleSearch} sx={{ display: 'flex', gap: 1, flexGrow: 1, maxWidth: 400 }}>
          <TextField
            size="small"
            placeholder={t('skinlib.searchPlaceholder')}
            value={tag}
            onChange={(e) => setTag(e.target.value)}
            fullWidth
            slotProps={{
              input: {
                startAdornment: (
                  <InputAdornment position="start">
                    <SearchIcon />
                  </InputAdornment>
                ),
              },
            }}
          />
        </Box>
      </Box>

      {error && (
        <Alert severity="error" sx={{ mb: 4 }}>
          {error}
        </Alert>
      )}

      {loading ? (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: 8 }}>
          <CircularProgress />
        </Box>
      ) : textures.length === 0 ? (
        <Box sx={{ textAlign: 'center', py: 8 }}>
          <Typography variant="body1" color="text.secondary">
            {t('skinlib.empty')}
          </Typography>
        </Box>
      ) : (
        <>
          <Grid container spacing={3}>
            {textures.map((item) => (
              <Grid size={{ xs: 12, sm: 6, md: 4, lg: 3 }} key={item.id}>
                <Card sx={{
                  height: '100%',
                  display: 'flex',
                  flexDirection: 'column',
                  transition: 'transform 0.2s',
                  '&:hover': {
                    transform: 'translateY(-4px)',
                    boxShadow: 4
                  }
                }}>
                  <Box sx={{
                    position: 'relative',
                    pt: item.type === 'skin' ? '150%' : '100%', // 皮肤预览通常较长
                    backgroundColor: 'grey.100',
                    overflow: 'hidden'
                  }}>
                    <CardMedia
                      component="img"
                      image={getPreviewUrl(item.preview_file)}
                      alt={item.name}
                      sx={{
                        position: 'absolute',
                        top: 0,
                        left: 0,
                        width: '100%',
                        height: '100%',
                        objectFit: 'contain',
                        p: 2,
                        imageRendering: 'pixelated' // 保持像素感
                      }}
                    />
                    <Chip
                      label={item.type === 'skin' ? (item.model === 'slim' ? 'Alex' : 'Steve') : 'Cape'}
                      size="small"
                      color={item.type === 'skin' ? 'primary' : 'secondary'}
                      sx={{ position: 'absolute', top: 8, right: 8 }}
                    />
                  </Box>
                  <CardContent sx={{ flexGrow: 1 }}>
                    <Typography variant="h6" component="div" noWrap gutterBottom>
                      {item.name}
                    </Typography>
                    <Typography variant="body2" color="text.secondary" sx={{
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      display: '-webkit-box',
                      WebkitLineClamp: 2,
                      WebkitBoxOrient: 'vertical',
                      height: '3em',
                      mb: 1
                    }}>
                      {item.description || t('skinlib.noDescription')}
                    </Typography>
                    <Stack direction="row" spacing={0.5} flexWrap="wrap">
                      {item.tags.split(/[，,；;\n]/).filter(t => t.trim()).slice(0, 3).map((t, idx) => (
                        <Chip key={idx} label={t.trim()} size="small" variant="outlined" />
                      ))}
                    </Stack>
                  </CardContent>
                  <CardActions sx={{ justifyContent: 'center', pb: 2, gap: 1 }}>
                    <Button
                      variant="contained"
                      size="small"
                      disabled={usingHash === item.hash || deletingHash === item.hash}
                      onClick={() => handleUseTexture(item)}
                    >
                      {usingHash === item.hash ? t('skinlib.using') : t('skinlib.use')}
                    </Button>
                    {String(item.uid) === getUid() && (
                      <Button
                        variant="outlined"
                        size="small"
                        color="error"
                        startIcon={<DeleteIcon />}
                        disabled={usingHash === item.hash || deletingHash === item.hash}
                        onClick={() => setConfirmDeleteItem(item)}
                      >
                        {t('skinlib.delete')}
                      </Button>
                    )}
                  </CardActions>
                </Card>
              </Grid>
            ))}
          </Grid>

          <Stack sx={{ mt: 6, alignItems: 'center' }}>
            <Pagination
              count={Math.ceil(total / 16)}
              page={page}
              onChange={handlePageChange}
              color="primary"
              size="large"
            />
          </Stack>
        </>
      )}

      <Dialog
        open={!!confirmDeleteItem}
        onClose={() => setConfirmDeleteItem(null)}
        maxWidth="xs"
        fullWidth
      >
        <DialogTitle>{t('skinlib.deleteConfirmTitle')}</DialogTitle>
        <DialogContent>
          <Typography>
            {confirmDeleteItem ? t('skinlib.deleteConfirm', { name: confirmDeleteItem.name }) : ''}
          </Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
            {t('skinlib.deleteConfirmHint')}
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmDeleteItem(null)} disabled={deletingHash !== null}>
            {t('common.cancel')}
          </Button>
          <Button
            variant="contained"
            color="error"
            onClick={() => confirmDeleteItem && handleDeleteTexture(confirmDeleteItem)}
            disabled={deletingHash !== null}
          >
            {deletingHash ? t('skinlib.deleting') : t('skinlib.deleteConfirmTitle')}
          </Button>
        </DialogActions>
      </Dialog>

      <Snackbar
        open={snackbar.open}
        autoHideDuration={4000}
        onClose={() => setSnackbar(prev => ({ ...prev, open: false }))}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
      >
        <Alert
          onClose={() => setSnackbar(prev => ({ ...prev, open: false }))}
          severity={snackbar.severity}
          variant="filled"
          sx={{ width: '100%' }}
        >
          {snackbar.message}
        </Alert>
      </Snackbar>

      <UploadDialog
        open={uploadDialogOpen}
        onClose={() => setUploadDialogOpen(false)}
        onSuccess={() => {
          fetchTextures();
          setPage(1);
        }}
      />
    </Box>
  );
};

export default Skinlib;