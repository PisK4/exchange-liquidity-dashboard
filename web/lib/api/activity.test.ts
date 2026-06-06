import { activityDecisionActions, activityEventsPath, normalizeActivityDecisionAction, type ActivityDecisionAction } from './activity';

const path = activityEventsPath({ platform: 'binance', review_status: 'pending', limit: 25 });
if (path !== '/api/activity/events?platform=binance&review_status=pending&limit=25') {
  throw new Error(path);
}

const action: ActivityDecisionAction = normalizeActivityDecisionAction('differentiate');
if (!activityDecisionActions.includes(action)) {
  throw new Error(action);
}
