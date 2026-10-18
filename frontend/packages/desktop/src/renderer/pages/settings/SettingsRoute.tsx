import React, { Suspense } from 'react';
import { Navigate, useParams } from 'react-router';
import AppLoader from '@renderer/components/layout/AppLoader';
import { isSettingsRouteId, settingsRouteLoaders, type SettingsRouteId } from './settingsRouteLoaders';
import './components/settings.css';

// Derive lazy pages from the one route registry; a new recovery route cannot
// leave a second manually maintained map behind as an undefined component.
const settingsPages = Object.fromEntries(
  Object.entries(settingsRouteLoaders).map(([id, loader]) => [id, React.lazy(loader)])
) as Record<SettingsRouteId, React.LazyExoticComponent<React.ComponentType>>;

const SettingsRoute: React.FC = () => {
  const { section } = useParams<{ section: string }>();
  if (!isSettingsRouteId(section)) return <Navigate to='/settings/experts' replace />;

  const Page = settingsPages[section];
  return (
    <Suspense key={section} fallback={<AppLoader />}>
      <Page />
    </Suspense>
  );
};

export default SettingsRoute;
