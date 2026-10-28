export const buildOrderQuery = ({ page, pageSize, scope, progress, instanceId, instanceName, applicant, startAt, endAt, keyword }) => ({
  page,
  page_size: pageSize,
  is_page: true,
  scope,
  progress,
  instance_id: instanceId,
  instance_name: instanceName,
  applicant,
  start_at: startAt,
  end_at: endAt,
  search: keyword,
})
