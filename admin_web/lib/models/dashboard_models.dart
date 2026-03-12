class DashboardOverview {
  const DashboardOverview({
    required this.usersCount,
    required this.activeUsersCount,
    required this.serversCount,
    required this.onlineServersCount,
    required this.groupsCount,
    required this.promocodesCount,
    required this.paymentsCount,
    required this.activeReleases,
    required this.adminsCount,
  });

  final int usersCount;
  final int activeUsersCount;
  final int serversCount;
  final int onlineServersCount;
  final int groupsCount;
  final int promocodesCount;
  final int paymentsCount;
  final int activeReleases;
  final int adminsCount;

  factory DashboardOverview.fromJson(Map<String, dynamic> json) {
    return DashboardOverview(
      usersCount: (json['users_count'] as num?)?.toInt() ?? 0,
      activeUsersCount: (json['active_users_count'] as num?)?.toInt() ?? 0,
      serversCount: (json['servers_count'] as num?)?.toInt() ?? 0,
      onlineServersCount: (json['online_servers_count'] as num?)?.toInt() ?? 0,
      groupsCount: (json['groups_count'] as num?)?.toInt() ?? 0,
      promocodesCount: (json['promocodes_count'] as num?)?.toInt() ?? 0,
      paymentsCount: (json['payments_count'] as num?)?.toInt() ?? 0,
      activeReleases: (json['active_releases'] as num?)?.toInt() ?? 0,
      adminsCount: (json['admins_count'] as num?)?.toInt() ?? 0,
    );
  }
}
