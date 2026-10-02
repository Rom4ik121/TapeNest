# bot-service migrations

bot-service keeps no persistent state in MVP (users live in `gateway.users`,
webhook dedupe lives in Redis with a 24 h TTL), so schema `bot` has no tables
yet. New tables go here as golang-migrate files (`000001_<name>.up.sql` /
`.down.sql`), schema-qualified with `bot.`.
