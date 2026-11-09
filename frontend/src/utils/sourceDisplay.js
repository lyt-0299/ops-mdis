import { _l } from './utils';

export const BUILTIN_PROMETHEUS_ID = 'builtin-prometheus';
export const BUILTIN_DIAGNOSE_MONGO_ID = 'builtin-diagnose-mongo';

export const getSourceDisplayName = (source) => {
  if (!source) return '-';
  const { id, type, name, builtin } = source;

  if (id === BUILTIN_PROMETHEUS_ID || (builtin && type === 'prometheus')) {
    return 'Prometheus';
  }

  if (id === BUILTIN_DIAGNOSE_MONGO_ID || (builtin && type === 'mongodb' && name === BUILTIN_DIAGNOSE_MONGO_ID)) {
    return 'MongoDB';
  }

  return name || id || '-';
};
