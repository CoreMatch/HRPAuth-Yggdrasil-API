export type TextureType = 'skin' | 'cape';
export type SkinModel = 'default' | 'slim';

export interface TextureItem {
  id: number;
  hash: string;
  type: TextureType;
  model?: SkinModel;
  uid: number;
  width: number;
  height: number;
  file_name: string;
  preview_file: string;
  created_at: string;
  updated_at: string;
}

export interface TextureUploadRequest {
  uuid: string;
  type: TextureType;
  model?: SkinModel;
  file: File;
}
