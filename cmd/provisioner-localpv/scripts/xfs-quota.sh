#!/usr/bin/env bash

# This script sets the limits on storage of hostpath volumes only for xfs fs

DIR="/data/"

set -u
# only exit with zero if all commands of the pipeline exit successfully
set -o pipefail

# Check if the script is run with sudo access
if [ "$(id -u)" != "0" ]; then
  echo "This script must be run with super-user privileges."
  exit 1
fi

# Check the fs of the dir where xfs quota is to be applied
FSTYPE=$(df -PTh $DIR | tail -n +2 | awk '{print $2}')
if [ "$FSTYPE" != "xfs" ]
then
  echo "Failed filesystem check: Wanted = "xfs" fs, Found = "$FSTYPE" fs."
  # Remove the provisioned volume
  rm -rf "$SUB_DIR_NAME"
  exit 0
else
  echo "Passed filesystem check: Wanted = "xfs" fs, Found = "$FSTYPE" fs."
fi

# Check if the prjquota option is enabled for the specified DIR
#ISPRJQUOTAENABELED=$(xfs_quota -x -c state $DIR)
ISPRJQUOTAENABELED=$(findmnt -lo options -T $DIR | tail -n 1)
if [ -z "$ISPRJQUOTAENABELED" ] || [[ "$ISPRJQUOTAENABELED" != *"prjquota"* && "$ISPRJQUOTAENABELED" != *"pquota"* ]]
then
  echo "Directory '$DIR' is not mounted with prjquota or pquota option"
  # Remove the provisioned volume
  rm -rf "$SUB_DIR_NAME"
  exit 1
else
  echo "Directory '$DIR' is mounted with prjquota option"
fi

# Get the last project id of the dir
LASTPROJECTID=$(xfs_quota -x -c 'report -h' $DIR | tail -2 | awk '{print $1}')
if [ $? -ne 0 ]
then
  echo "Error in getting the last project id."
  # Remove the provisioned volume
  rm -rf "$SUB_DIR_NAME"
  exit 1
else
  echo "Successfully retrieved last project id. Last projectID: $LASTPROJECTID"
fi

LASTPROJECTIDNUMBER=${LASTPROJECTID:1}

# Form the new ProjectID to be initialised
NEWPROJECTID=`expr $LASTPROJECTIDNUMBER + 1`
echo $LASTPROJECTID
echo $LASTPROJECTIDNUMBER
echo $NEWPROJECTID

# Create quota project
xfs_quota -x -c "project -s -p $DIR$SUB_DIR_NAME $NEWPROJECTID" $DIR
if [ $? -ne 0 ]
then
  echo "Error in creating the quota project."
  # Remove the provisioned volume
  rm -rf "$SUB_DIR_NAME"
  exit 1
else
  echo "Successfully created quota project with id '$NEWPROJECTID'."
fi

# Check if the limits are empty
if [[ -z "$BSOFT_LIMIT" && -z "$BHARD_LIMIT" ]]
then
  echo "ERROR: limits cannot be empty"
  # Remove the provisioned volume
  rm -rf "$SUB_DIR_NAME"
  exit 1
elif [ -z "$BSOFT_LIMIT" ]
then
  # Set the quota with only the hard limit on the project created above
  xfs_quota -x -c "limit -p bhard=$BHARD_LIMIT $NEWPROJECTID" $DIR
elif [ -z "$BHARD_LIMIT" ]
then
  # Set the quota with soft limit on the project created above
  xfs_quota -x -c "limit -p bsoft=$BSOFT_LIMIT $NEWPROJECTID" $DIR
else
  # Set the quota with soft and hard limit both on the project created above
  xfs_quota -x -c "limit -p bsoft=$BSOFT_LIMIT bhard=$BHARD_LIMIT $NEWPROJECTID" $DIR
fi
if [ $? -ne 0 ]
then
  echo "Error in setting the quota limit for the project '$NEWPROJECTID'."
  # Remove the provisioned volume
  rm -rf "$SUB_DIR_NAME"
  exit 1
else
  echo "Successfully set quota limit for the project '$NEWPROJECTID'."
fi
