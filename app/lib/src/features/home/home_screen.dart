import 'package:flutter/material.dart';

import '../../theme/app_theme.dart';

/// P0 shell: the delivery status of the build, and the identity the later
/// role-based home screens will share. Replaced by the member and staff homes
/// in P2-P6.
class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  static const List<({String title, String detail, IconData icon})> _phases =
      <({String title, String detail, IconData icon})>[
    (
      title: 'P0 — Plan and skeleton',
      detail: 'Architecture, data model, state machines, RBAC matrix, threat model, ADRs and assumptions delivered for approval.',
      icon: Icons.assignment_turned_in_outlined,
    ),
    (
      title: 'P1 — Platform core',
      detail: 'Authentication, sessions, row-level security, audit chain, idempotency and storage.',
      icon: Icons.shield_outlined,
    ),
    (
      title: 'P2–P6 — Domain modules',
      detail: 'Registration, meetings and attendance, payments and reconciliation, group borrowing, dashboards and ranking.',
      icon: Icons.groups_outlined,
    ),
    (
      title: 'P7–P10 — Engagement and delivery',
      detail: 'Learning, comments, disputes, tasks, offline sync, hardening, deployment and handover.',
      icon: Icons.rocket_launch_outlined,
    ),
  ];

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Yopsa Pamoja')),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 720),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: <Widget>[
                Text(
                  'Stronger groups. Informed decisions.',
                  style: theme.textTheme.titleMedium?.copyWith(color: brandGreen),
                ),
                const SizedBox(height: 4),
                Text(
                  'Group management and financial monitoring for VSLAs, investment clubs, associations, cooperatives and SACCOs.',
                  style: theme.textTheme.bodyMedium,
                ),
                const SizedBox(height: 20),
                ..._phases.map(
                  (({String title, String detail, IconData icon}) phase) => Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: Card(
                      child: ListTile(
                        leading: Icon(phase.icon, color: brandBlue),
                        title: Text(phase.title, style: theme.textTheme.titleSmall),
                        subtitle: Padding(
                          padding: const EdgeInsets.only(top: 4),
                          child: Text(phase.detail, style: theme.textTheme.bodySmall),
                        ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 12),
                Text(
                  'Build 0.1.0 · client shell. Sign-in and role-based home screens arrive with the platform core.',
                  style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
