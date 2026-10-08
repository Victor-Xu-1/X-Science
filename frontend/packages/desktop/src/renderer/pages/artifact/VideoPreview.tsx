import React from 'react';
import MediaPreview from '@/renderer/pages/conversation/Preview/components/viewers/MediaPreview';

type VideoPreviewProps = {
  url: string;
  filename: string;
};

const VideoPreview: React.FC<VideoPreviewProps> = ({ url, filename }) => (
  <MediaPreview mediaType='video' filename={filename} content={url} />
);

export default VideoPreview;
