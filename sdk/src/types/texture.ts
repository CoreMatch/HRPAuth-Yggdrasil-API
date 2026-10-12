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
  name: string;
  description: string;
  tags: string;
  created_at: string;
  updated_at: string;
}

export interface TextureUploadRequest {
  uid: number;
  type: TextureType;
  model?: SkinModel;
  name: string;
  description?: string;
  tags?: string;
  file: File;
}

export interface TextureListRequest {
  type?: 'all' | 'default' | 'slim' | 'cape';
  order?: 'desc' | 'asc';
  tag?: string;
  page?: number;
}

export interface TextureListResponse {
  items: TextureItem[];
  filter: string;
  order: string;
  tag: string;
  page: number;
  page_size: number;
  total: number;
  has_more: boolean;
}
