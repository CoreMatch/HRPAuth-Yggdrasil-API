import { useEffect, useRef } from 'react';
import { SkinViewer } from 'skinview3d';

interface SkinViewer3DProps {
  skinUrl?: string | null;
  capeUrl?: string | null;
  width?: number;
  height?: number;
}

const SIDE_BACK_ROTATION = Math.PI * 0.72;

async function loadImageFromUrl(url: string): Promise<{
  image: HTMLImageElement;
  revoke: () => void;
}> {
  const response = await fetch(url, { cache: 'no-store' });
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`);
  }

  const blob = await response.blob();
  const objectUrl = URL.createObjectURL(blob);
  const image = new Image();

  return new Promise((resolve, reject) => {
    image.onload = () => {
      resolve({
        image,
        revoke: () => URL.revokeObjectURL(objectUrl),
      });
    };
    image.onerror = () => {
      URL.revokeObjectURL(objectUrl);
      reject(new Error('图片解码失败'));
    };
    image.src = objectUrl;
  });
}

function normalizeCapeTexture(image: HTMLImageElement): HTMLImageElement | HTMLCanvasElement {
  const width = image.naturalWidth || image.width;
  const height = image.naturalHeight || image.height;

  if (width === height * 2) {
    return image;
  }

  let scale: number | null = null;
  if (width * 17 === height * 22) {
    scale = width / 22;
  } else if (width * 11 === height * 23) {
    scale = width / 46;
  }

  if (!scale) {
    throw new Error(`不支持的披风尺寸: ${width}x${height}`);
  }

  const canvas = document.createElement('canvas');
  canvas.width = 64 * scale;
  canvas.height = 32 * scale;
  const context = canvas.getContext('2d');
  if (!context) {
    throw new Error('无法创建披风纹理画布');
  }

  context.clearRect(0, 0, canvas.width, canvas.height);
  context.drawImage(image, 0, 0, width, height);
  return canvas;
}

export default function SkinViewer3D({
  skinUrl,
  capeUrl,
  width = 200,
  height = 400,
}: SkinViewer3DProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const viewerRef = useRef<SkinViewer | null>(null);

  useEffect(() => {
    if (!canvasRef.current) return;

    // 诊断：用临时 canvas 检测 WebGL 支持（不影响目标 canvas 的 context）
    const testCanvas = document.createElement('canvas');
    const gl = testCanvas.getContext('webgl2') || testCanvas.getContext('webgl');
    console.log('[SkinViewer3D] WebGL 支持:', gl ? 'OK' : '不支持', gl ? gl.getParameter(gl.RENDERER) : '');

    const viewer = new SkinViewer({
      canvas: canvasRef.current,
      width,
      height,
    });

    viewer.autoRotate = true;
    viewer.autoRotateSpeed = 0.5;
    // skinview3d v3 默认相机在 z=1；将相机拉到 z=50 会让模型缩小到几乎不可见。
    // 改用 zoom 控制取景，使全身（高约 1.8 单位）在竖版画布中完整可见。
    viewer.zoom = 0.5;
    // 首屏展示侧后方视角，让皮肤和披风都能被直接看到。
    viewer.playerWrapper.rotation.y = SIDE_BACK_ROTATION;

    viewerRef.current = viewer;

    return () => {
      viewer.dispose();
      viewerRef.current = null;
    };
  }, [width, height]);

  useEffect(() => {
    if (!viewerRef.current) return;

    let disposed = false;
    let revoke: (() => void) | null = null;

    const applySkin = async () => {
      if (!viewerRef.current) return;

      if (!skinUrl) {
        viewerRef.current.loadSkin(null);
        return;
      }

      try {
        const loaded = await loadImageFromUrl(skinUrl);
        if (disposed) {
          loaded.revoke();
          return;
        }

        revoke = loaded.revoke;
        viewerRef.current.loadSkin(loaded.image);
      } catch (err) {
        console.error('[SkinViewer3D] 皮肤纹理加载失败:', skinUrl, err);
      }
    };

    void applySkin();

    return () => {
      disposed = true;
      revoke?.();
    };
  }, [skinUrl]);

  useEffect(() => {
    if (!viewerRef.current) return;

    let disposed = false;
    let revoke: (() => void) | null = null;

    const applyCape = async () => {
      if (!viewerRef.current) return;

      if (!capeUrl) {
        viewerRef.current.loadCape(null);
        return;
      }

      try {
        const loaded = await loadImageFromUrl(capeUrl);
        if (disposed) {
          loaded.revoke();
          return;
        }

        revoke = loaded.revoke;
        const normalizedCape = normalizeCapeTexture(loaded.image);
        viewerRef.current.loadCape(normalizedCape, { backEquipment: 'cape' });
        viewerRef.current.playerObject.backEquipment = 'cape';
      } catch (err) {
        console.error('[SkinViewer3D] 披风纹理加载失败:', capeUrl, err);
      }
    };

    void applyCape();

    return () => {
      disposed = true;
      revoke?.();
    };
  }, [capeUrl]);

  return (
    <canvas
      ref={canvasRef}
      width={width}
      height={height}
      style={{ imageRendering: 'pixelated' }}
    />
  );
}
