// Reads `redocly lint --format=json` from stdin and fails on any problem,
// warnings included (Redocly itself only fails on errors).
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => (input += chunk));
process.stdin.on('end', () => {
  let report;
  try {
    report = JSON.parse(input);
  } catch (err) {
    console.error('api:lint: could not read the Redocly report: ' + err.message);
    console.error(input);
    process.exit(2);
  }
  const problems = report.problems || [];
  for (const p of problems) {
    const where = (p.location && p.location[0] && p.location[0].source && p.location[0].source.absoluteRef) || '';
    const pointer = (p.location && p.location[0] && p.location[0].pointer) || '';
    console.error(`${p.severity}  ${p.ruleId}  ${p.message}\n    ${where} ${pointer}`);
  }
  const { errors = 0, warnings = 0 } = report.totals || {};
  if (errors + warnings > 0) {
    console.error(`api:lint: ${errors} error(s), ${warnings} warning(s); warnings fail the lint too`);
    process.exit(1);
  }
  console.log('api:lint: OpenAPI source is clean');
});
