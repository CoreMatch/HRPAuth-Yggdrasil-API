import { useState, useEffect } from 'react';
import { Box, Card, CardContent, Grid, Button, Typography } from "@mui/material";
import ContentCopyIcon from "@mui/icons-material/ContentCopy";
import { useSdkTranslation } from '../hooks/useSdkTranslation';
import { getRealBackendUrl, buildServiceUrl } from '../utils/proxy';
import ProfileCreationContent from '../components/ProfileCreationContent';

function CodeBlock({ children }: { children: string }) {
  return (
    <Box
      sx={{
        position: "relative",
        bgcolor: "grey.900",
        color: "grey.100",
        p: 2,
        borderRadius: 2,
        fontFamily: "monospace",
        fontSize: "0.9rem",
        whiteSpace: "pre-wrap",
        border: "1px solid",
        borderColor: "grey.800",
      }}
    >
      {children}
    </Box>
  );
}

export default function YggdrasilApi() {
  const { t } = useSdkTranslation();
  const [baseUrl, setBaseUrl] = useState('');
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let mounted = true;
    getRealBackendUrl().then((url: string) => {
      if (mounted) setBaseUrl(buildServiceUrl(url, '').replace(/\/$/, ''));
    });
    return () => {
      mounted = false;
    };
  }, []);

  const handleCopy = async () => {
    if (!baseUrl) return;
    await navigator.clipboard.writeText(baseUrl);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <Typography variant="h6">{t('yggdrasil.intro')}</Typography>

      <Box>
        <Typography variant="h6" gutterBottom>{t('profileCreation.title')}</Typography>
        <ProfileCreationContent />
      </Box>

      <Grid container spacing={2}>
        <Grid size={{ xs: 12, md: 6 }}>
          <Card sx={{ height: '100%' }}>
            <CardContent>
              <Typography variant="subtitle2" color="text.secondary" gutterBottom>{t('yggdrasil.serverAddress')}</Typography>
              <CodeBlock>{baseUrl}</CodeBlock>
              <Button
                variant="contained"
                startIcon={<ContentCopyIcon />}
                onClick={handleCopy}
                sx={{ mt: 2 }}
                fullWidth
              >
                {copied ? t('yggdrasil.copied') : t('yggdrasil.copyUrl')}
              </Button>
            </CardContent>
          </Card>
        </Grid>
      </Grid>

      <Card>
        <CardContent>
          <Typography variant="subtitle2" color="text.secondary" gutterBottom>{t('yggdrasil.usageInstructions')}</Typography>
          <Typography variant="body2" component="div">
            <Box component="ol" sx={{ pl: 2, m: 0 }}>
              <li>{t('yggdrasil.steps.1')}</li>
              <li>{t('yggdrasil.steps.2')}</li>
              <li>{t('yggdrasil.steps.3')}</li>
            </Box>
          </Typography>
        </CardContent>
      </Card>
    </Box>
  );
}
