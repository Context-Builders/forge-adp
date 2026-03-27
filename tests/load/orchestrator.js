import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '1m', target: 10 },
    { duration: '3m', target: 50 },
    { duration: '1m', target: 0 },
  ],
};

export default function () {
  const task = {
    jira_ticket_id: `LOAD-${__VU}-${__ITER}`,
    agent_role: 'backend-developer',
    skill_name: 'api-implementation',
    priority: 1,
    input: { description: 'Load test task' },
  };

  const response = http.post(
    'http://localhost:19080/api/v1/tasks',
    JSON.stringify(task),
    { headers: { 'Content-Type': 'application/json' } }
  );

  check(response, {
    'status is 201': (r) => r.status === 201,
    'response time < 200ms': (r) => r.timings.duration < 200,
  });

  sleep(1);
}
