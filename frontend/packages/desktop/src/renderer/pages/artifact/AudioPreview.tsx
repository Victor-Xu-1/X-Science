import React from 'react';
import MediaPreview from '@/renderer/pages/conversation/Preview/components/viewers/MediaPreview';

type AudioPreviewProps = {
  url: string;
  filename: string;
};

const AudioPreview: React.FC<AudioPreviewProps> = ({ url, filename }) => (
  <MediaPreview mediaType='audio' filename={filename} content={url} />
);

export default AudioPreview;
