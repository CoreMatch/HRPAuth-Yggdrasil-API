import { useEffect, useState } from 'react';
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  FormControl,
  FormControlLabel,
  Radio,
  RadioGroup,
  Stack,
  TextField,
  Typography,
} from '@mui/material';

import { useSdkTranslation } from '../hooks/useSdkTranslation';
import {
  createGameProfile,
  getGameProfile,
  type GameProfile,
  type ProfileCreationMode,
} from '../api/user';

export default function ProfileCreationContent() {
  const { t } = useSdkTranslation();
  const [mode, setMode] = useState<ProfileCreationMode>('random');
  const [uuid, setUuid] = useState('');
  const [username, setUsername] = useState('');
  const [profile, setProfile] = useState<GameProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [canCreate, setCanCreate] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    getGameProfile()
      .then((response) => {
        if (!active) return;
        if (response.success && response.data) {
          setCanCreate(true);
          setProfile(response.data.profile);
        } else {
          setError(response.message || t('profileCreation.loadFailed'));
        }
      })
      .catch(() => {
        if (active) setError(t('profileCreation.loadFailed'));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [t]);

  const handleCreate = async () => {
    setSaving(true);
    setError(null);
    try {
      const input = mode === 'custom'
        ? { mode, uuid }
        : mode === 'mojang'
          ? { mode, username: username.trim() }
          : { mode };
      const response = await createGameProfile(input);
      if (response.success && response.data) {
        setProfile(response.data);
      } else {
        setError(response.message || t('profileCreation.createFailed'));
      }
    } catch {
      setError(t('profileCreation.createFailed'));
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return <Box sx={{ display: 'flex', justifyContent: 'center', py: 2 }}><CircularProgress size={24} /></Box>;
  }

  if (profile) {
    return (
      <Stack spacing={1}>
        <Alert severity="success">{t('profileCreation.created')}</Alert>
        <Typography variant="body2">
          {t('profileCreation.name')}: <strong>{profile.name}</strong>
        </Typography>
        <Typography variant="body2" sx={{ overflowWrap: 'anywhere', fontFamily: 'monospace' }}>
          {t('profileCreation.uuid')}: {profile.id}
        </Typography>
      </Stack>
    );
  }

  if (!canCreate) {
    return <Alert severity="error">{error || t('profileCreation.loadFailed')}</Alert>;
  }

  return (
    <Stack spacing={2}>
      <Typography variant="body2" color="text.secondary">
        {t('profileCreation.description')}
      </Typography>
      {error && <Alert severity="error" onClose={() => setError(null)}>{error}</Alert>}
      <FormControl>
        <RadioGroup
          value={mode}
          onChange={(event) => setMode(event.target.value as ProfileCreationMode)}
        >
          <FormControlLabel value="random" control={<Radio />} label={t('profileCreation.random')} />
          <FormControlLabel value="custom" control={<Radio />} label={t('profileCreation.custom')} />
          <FormControlLabel value="mojang" control={<Radio />} label={t('profileCreation.mojang')} />
        </RadioGroup>
      </FormControl>
      {mode === 'custom' && (
        <TextField
          label={t('profileCreation.uuid')}
          value={uuid}
          onChange={(event) => setUuid(event.target.value)}
          placeholder="00000000000000000000000000000000"
          helperText={t('profileCreation.customHint')}
          inputProps={{ maxLength: 36, autoComplete: 'off' }}
          fullWidth
        />
      )}
      {mode === 'mojang' && (
        <TextField
          label={t('profileCreation.mojangUsername')}
          value={username}
          onChange={(event) => setUsername(event.target.value)}
          helperText={t('profileCreation.mojangHint')}
          inputProps={{ maxLength: 16, autoComplete: 'off' }}
          fullWidth
        />
      )}
      <Button
        variant="contained"
        onClick={handleCreate}
        disabled={saving || (mode === 'custom' && !uuid.trim()) || (mode === 'mojang' && !username.trim())}
      >
        {saving ? t('profileCreation.creating') : t('profileCreation.create')}
      </Button>
    </Stack>
  );
}
